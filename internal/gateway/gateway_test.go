package gateway

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPeekClientHello(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		c := tls.Client(a, &tls.Config{ServerName: "vm1.servers.example.com", InsecureSkipVerify: true})
		c.Handshake() // fails, we never answer
	}()
	b.SetDeadline(time.Now().Add(5 * time.Second))
	sni, hello, err := peekClientHello(b)
	if err != nil || sni != "vm1.servers.example.com" || len(hello) < 50 || hello[0] != 0x16 {
		t.Fatalf("sni=%q len=%d err=%v", sni, len(hello), err)
	}
	a.Close()

	a, b = net.Pipe()
	go func() { a.Write([]byte("GET / HTTP/1.1\r\n\r\n")); a.Close() }()
	if _, _, err := peekClientHello(b); err == nil {
		t.Fatal("plain HTTP must fail")
	}
}

func echoServer(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestTCPRoutes(t *testing.T) {
	g := New(Config{RateLimit: 0}, slog.New(slog.DiscardHandler))
	defer g.Close()
	target := echoServer(t)
	port := freePort(t)
	g.SetRoutes([]TCPRoute{{Port: port, Bind: "127.0.0.1", Target: target}}, nil)

	addr := "127.0.0.1:" + strconv.Itoa(port)
	roundTrip := func() (string, error) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return "", err
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		c.Write([]byte("ping"))
		buf := make([]byte, 4)
		_, err = io.ReadFull(c, buf)
		return string(buf), err
	}
	if s, err := roundTrip(); err != nil || s != "ping" {
		t.Fatalf("got %q %v", s, err)
	}
	// Allowlist that excludes 127.0.0.1: connection is closed without data.
	g.SetRoutes([]TCPRoute{{Port: port, Bind: "127.0.0.1", Target: target, Allowlist: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}}, nil)
	if s, err := roundTrip(); err == nil {
		t.Fatalf("allowlist should block, got %q", s)
	}
	// Removing the route closes the listener.
	g.SetRoutes(nil, nil)
	if _, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		t.Fatal("port should be closed")
	}
}

func TestRateLimiter(t *testing.T) {
	r := newRateLimiter(10) // burst 5
	now := time.Now()
	n := 0
	for i := 0; i < 20; i++ {
		if r.allow("1.2.3.4", now) {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("burst allowed %d", n)
	}
	if !r.allow("1.2.3.4", now.Add(time.Minute)) || !r.allow("5.6.7.8", now) {
		t.Fatal("refill / other ip")
	}
	if (*rateLimiter)(nil).allow("x", now) != true {
		t.Fatal("nil limiter allows")
	}
}

func TestHTTPAndHTTPSRouting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("device:" + r.Host))
	}))
	defer backend.Close()
	tlsBackend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tls-device"))
	}))
	defer tlsBackend.Close()

	dash := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("dashboard")) })
	g := New(Config{HTTPListen: "127.0.0.1:0", HTTPSListen: "127.0.0.1:0", DashboardHost: "dash.example.com", DashboardHandler: dash}, slog.New(slog.DiscardHandler))
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.SetRoutes(nil, []HTTPRoute{{
		Host:        "vm1.servers.example.com",
		HTTPTarget:  strings.TrimPrefix(backend.URL, "http://"),
		HTTPSTarget: strings.TrimPrefix(tlsBackend.URL, "https://"),
	}})

	get := func(host string) (int, string) {
		req, _ := http.NewRequest("GET", "http://"+g.HTTPAddr()+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("vm1.servers.example.com"); code != 200 || body != "device:vm1.servers.example.com" {
		t.Fatalf("device route: %d %q", code, body)
	}
	if _, body := get("dash.example.com"); body != "dashboard" {
		t.Fatalf("dashboard: %q", body)
	}
	if code, _ := get("unknown.example.com"); code != 404 {
		t.Fatalf("unknown host: %d", code)
	}

	// HTTPS passthrough by SNI.
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "vm1.servers.example.com"},
		DialContext: (&net.Dialer{}).DialContext}
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get("https://" + g.HTTPSAddr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "tls-device" {
		t.Fatalf("https passthrough: %q", b)
	}
}
