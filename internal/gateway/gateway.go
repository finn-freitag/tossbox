// Package gateway forwards public traffic on the dashboard host to devices on
// the nodes (DESIGN.md §12.4/§12.5):
//
//   - one TCP listener per exposed service port, proxied to node.address:port
//     (the node agent listens on the same port number),
//   - HTTP on :80 routed by Host header to the device's http service,
//   - HTTPS on :443 routed by SNI (TLS passthrough) to the device's https service,
//   - the dashboard host and guacamole.<base domain> on :80/:443, with TLS
//     terminated via ACME (autocert) when configured.
//
// Routes are replaced at runtime with SetRoutes.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// TCPRoute forwards one public port.
type TCPRoute struct {
	Port      int
	Bind      string // "0.0.0.0" or the tailscale address
	Target    string // node.address:port
	Allowlist []netip.Prefix
	DeviceID  string
}

// HTTPRoute maps a device FQDN to its http/https targets (empty = not exposed).
type HTTPRoute struct {
	Host        string
	HTTPTarget  string
	HTTPSTarget string
	Allowlist   []netip.Prefix
}

// Config of the gateway.
type Config struct {
	HTTPListen    string
	HTTPSListen   string
	FallbackHTTP  string
	FallbackHTTPS string
	RateLimit     int // per IP per minute, 0 = off

	DashboardHost    string
	DashboardHandler http.Handler

	GuacamoleHost     string // empty = disabled
	GuacamoleUpstream string // http://127.0.0.1:8081
	GuacamolePath     string // e.g. /guacamole, for redirecting "/"

	// ACME terminates TLS for the dashboard and guacamole hosts. nil = those
	// hosts are only served over plain HTTP on :80.
	ACME *autocert.Manager
}

// Gateway is the traffic forwarder.
type Gateway struct {
	cfg     Config
	log     *slog.Logger
	limiter *rateLimiter

	mu       sync.RWMutex
	tcp      map[int]*tcpListener
	httpByFQ map[string]HTTPRoute

	httpSrv   *http.Server
	tlsSrv    *http.Server
	tlsLn     *chanListener
	httpsLn   net.Listener
	httpLn    net.Listener
	guacProxy http.Handler
	closed    chan struct{}
	closeOnce sync.Once
}

// New creates a gateway (call Start to listen on :80/:443).
func New(cfg Config, log *slog.Logger) *Gateway {
	g := &Gateway{
		cfg:      cfg,
		log:      log.With("component", "gateway"),
		limiter:  newRateLimiter(cfg.RateLimit),
		tcp:      map[int]*tcpListener{},
		httpByFQ: map[string]HTTPRoute{},
		closed:   make(chan struct{}),
	}
	if cfg.GuacamoleHost != "" && cfg.GuacamoleUpstream != "" {
		if u, err := url.Parse(cfg.GuacamoleUpstream); err == nil {
			g.guacProxy = newReverseProxy(u, false)
		}
	}
	return g
}

// Start opens the HTTP and HTTPS listeners (if configured).
func (g *Gateway) Start() error {
	if g.cfg.HTTPListen != "" {
		ln, err := net.Listen("tcp", g.cfg.HTTPListen)
		if err != nil {
			return err
		}
		g.httpLn = ln
		g.httpSrv = &http.Server{Handler: http.HandlerFunc(g.serveHTTP), ReadHeaderTimeout: 15 * time.Second}
		go g.httpSrv.Serve(ln)
		g.log.Info("http routing listening", "addr", ln.Addr().String())
	}
	if g.cfg.HTTPSListen != "" {
		ln, err := net.Listen("tcp", g.cfg.HTTPSListen)
		if err != nil {
			return err
		}
		g.httpsLn = ln
		g.tlsLn = newChanListener(ln.Addr())
		g.tlsSrv = &http.Server{Handler: http.HandlerFunc(g.serveTerminated), ReadHeaderTimeout: 15 * time.Second}
		go g.tlsSrv.Serve(g.tlsLn)
		go g.acceptHTTPS(ln)
		g.log.Info("https routing listening", "addr", ln.Addr().String())
	}
	return nil
}

// HTTPAddr / HTTPSAddr return the bound addresses (tests).
func (g *Gateway) HTTPAddr() string  { return addrOf(g.httpLn) }
func (g *Gateway) HTTPSAddr() string { return addrOf(g.httpsLn) }

func addrOf(ln net.Listener) string {
	if ln == nil {
		return ""
	}
	return ln.Addr().String()
}

// Close stops everything.
func (g *Gateway) Close() {
	g.closeOnce.Do(g.close)
}

func (g *Gateway) close() {
	close(g.closed)
	if g.httpSrv != nil {
		g.httpSrv.Close()
	}
	if g.httpsLn != nil {
		g.httpsLn.Close()
	}
	if g.tlsSrv != nil {
		g.tlsSrv.Close()
	}
	g.mu.Lock()
	for p, l := range g.tcp {
		l.close()
		delete(g.tcp, p)
	}
	g.mu.Unlock()
}

// SetRoutes replaces all routes. TCP listeners are opened, rebound or closed
// as needed; a port whose bind address is unchanged keeps its listener and
// only gets the new target/allowlist.
func (g *Gateway) SetRoutes(tcp []TCPRoute, http []HTTPRoute) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.httpByFQ = make(map[string]HTTPRoute, len(http))
	for _, r := range http {
		g.httpByFQ[strings.ToLower(r.Host)] = r
	}

	want := make(map[int]TCPRoute, len(tcp))
	for _, r := range tcp {
		want[r.Port] = r
	}
	for port, l := range g.tcp {
		r, ok := want[port]
		if !ok || r.Bind != l.bind {
			l.close()
			delete(g.tcp, port)
		}
	}
	for port, r := range want {
		if l, ok := g.tcp[port]; ok {
			l.setRoute(r)
			continue
		}
		l, err := g.listenTCP(r)
		if err != nil {
			g.log.Error("open port", "port", port, "bind", r.Bind, "err", err)
			continue
		}
		g.tcp[port] = l
	}
}

// ---------------------------------------------------------------- TCP

type tcpListener struct {
	g    *Gateway
	bind string
	ln   net.Listener

	mu    sync.Mutex
	route TCPRoute
	conns map[net.Conn]struct{}
	done  bool
}

func (g *Gateway) listenTCP(r TCPRoute) (*tcpListener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(r.Bind, strconv.Itoa(r.Port)))
	if err != nil {
		return nil, err
	}
	l := &tcpListener{g: g, bind: r.Bind, ln: ln, route: r, conns: map[net.Conn]struct{}{}}
	go l.serve()
	return l, nil
}

func (l *tcpListener) setRoute(r TCPRoute) {
	l.mu.Lock()
	l.route = r
	l.mu.Unlock()
}

func (l *tcpListener) close() {
	l.mu.Lock()
	l.done = true
	for c := range l.conns {
		c.Close()
	}
	l.mu.Unlock()
	l.ln.Close()
}

func (l *tcpListener) serve() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return
		}
		l.mu.Lock()
		r := l.route
		if l.done {
			l.mu.Unlock()
			c.Close()
			return
		}
		l.conns[c] = struct{}{}
		l.mu.Unlock()
		go func() {
			defer func() {
				l.mu.Lock()
				delete(l.conns, c)
				l.mu.Unlock()
			}()
			l.g.handleTCP(c, r)
		}()
	}
}

func (g *Gateway) handleTCP(c net.Conn, r TCPRoute) {
	defer c.Close()
	ip := remoteIP(c)
	if !allowed(r.Allowlist, ip) {
		return
	}
	if !g.limiter.allow(ip.String(), time.Now()) {
		g.log.Warn("rate limited", "ip", ip.String(), "port", r.Port)
		return
	}
	pipe(c, r.Target, nil)
}

func remoteIP(c net.Conn) netip.Addr {
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		if a, ok := netip.AddrFromSlice(ta.IP); ok {
			return a.Unmap()
		}
	}
	return netip.Addr{}
}

func allowed(list []netip.Prefix, ip netip.Addr) bool {
	if len(list) == 0 {
		return true
	}
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// pipe dials target, writes prefix, and copies in both directions until both
// sides are done.
func pipe(c net.Conn, target string, prefix []byte) {
	up, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return
	}
	defer up.Close()
	if len(prefix) > 0 {
		if _, err := up.Write(prefix); err != nil {
			return
		}
	}
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		} else {
			dst.Close()
		}
		done <- struct{}{}
	}
	go cp(up, c)
	go cp(c, up)
	<-done
	<-done
}

// ---------------------------------------------------------------- HTTP :80

func hostOnly(h string) string {
	h = strings.ToLower(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.TrimSuffix(h, ".")
}

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	switch {
	case host != "" && host == g.cfg.DashboardHost:
		if g.cfg.ACME != nil {
			g.cfg.ACME.HTTPHandler(nil).ServeHTTP(w, r) // ACME challenge, else redirect to https
			return
		}
		g.cfg.DashboardHandler.ServeHTTP(w, r)
		return
	case host != "" && host == g.cfg.GuacamoleHost && g.guacProxy != nil:
		if g.cfg.ACME != nil {
			g.cfg.ACME.HTTPHandler(nil).ServeHTTP(w, r)
			return
		}
		g.serveGuacamole(w, r)
		return
	}
	g.mu.RLock()
	route, ok := g.httpByFQ[host]
	g.mu.RUnlock()
	if ok && route.HTTPTarget != "" {
		ip, _ := netip.ParseAddrPort(r.RemoteAddr)
		if !allowed(route.Allowlist, ip.Addr().Unmap()) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		newReverseProxy(&url.URL{Scheme: "http", Host: route.HTTPTarget}, true).ServeHTTP(w, r)
		return
	}
	if g.cfg.FallbackHTTP != "" {
		newReverseProxy(&url.URL{Scheme: "http", Host: g.cfg.FallbackHTTP}, true).ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// newReverseProxy proxies to target. keepHost preserves the original Host
// header (devices and fallback proxies route by it).
func newReverseProxy(target *url.URL, keepHost bool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			if keepHost {
				pr.Out.Host = pr.In.Host
			}
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
}

func (g *Gateway) serveGuacamole(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && g.cfg.GuacamolePath != "" && g.cfg.GuacamolePath != "/" {
		http.Redirect(w, r, strings.TrimRight(g.cfg.GuacamolePath, "/")+"/", http.StatusFound)
		return
	}
	g.guacProxy.ServeHTTP(w, r)
}

// ---------------------------------------------------------------- HTTPS :443

func (g *Gateway) acceptHTTPS(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-g.closed:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go g.handleHTTPS(c)
	}
}

func (g *Gateway) handleHTTPS(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	sni, hello, err := peekClientHello(c)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	sni = strings.ToLower(sni)
	pc := &prefixConn{Conn: c, prefix: hello}

	terminate := g.cfg.ACME != nil && sni != "" &&
		(sni == g.cfg.DashboardHost || (sni == g.cfg.GuacamoleHost && g.guacProxy != nil))
	if terminate {
		cfg := g.cfg.ACME.TLSConfig()
		// HTTP/1.1 only: the terminated connections are served through a
		// plain http.Server on a channel listener.
		cfg.NextProtos = []string{"http/1.1", acme.ALPNProto}
		if !g.tlsLn.push(tls.Server(pc, cfg)) {
			c.Close()
		}
		return
	}

	defer c.Close()
	g.mu.RLock()
	route, ok := g.httpByFQ[sni]
	g.mu.RUnlock()
	if ok && route.HTTPSTarget != "" {
		if !allowed(route.Allowlist, remoteIP(c)) {
			return
		}
		pipe(c, route.HTTPSTarget, hello)
		return
	}
	if g.cfg.FallbackHTTPS != "" {
		pipe(c, g.cfg.FallbackHTTPS, hello)
	}
}

// serveTerminated handles requests on TLS connections terminated by the gateway.
func (g *Gateway) serveTerminated(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	if r.TLS != nil && r.TLS.ServerName != "" {
		host = strings.ToLower(r.TLS.ServerName)
	}
	switch host {
	case g.cfg.DashboardHost:
		g.cfg.DashboardHandler.ServeHTTP(w, r)
	case g.cfg.GuacamoleHost:
		g.serveGuacamole(w, r)
	default:
		http.NotFound(w, r)
	}
}

// Shutdown gracefully stops the HTTP servers.
func (g *Gateway) Shutdown(ctx context.Context) {
	if g.httpSrv != nil {
		g.httpSrv.Shutdown(ctx)
	}
	g.Close()
}
