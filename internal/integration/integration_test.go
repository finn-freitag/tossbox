package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finn-freitag/tb-proto/enroll"
	agentv1 "github.com/finn-freitag/tb-proto/gen/agent/v1"
	"github.com/finn-freitag/tossbox/internal/api"
	"github.com/finn-freitag/tossbox/internal/catalog"
	"github.com/finn-freitag/tossbox/internal/config"
	"github.com/finn-freitag/tossbox/internal/core"
	"github.com/finn-freitag/tossbox/internal/events"
	"github.com/finn-freitag/tossbox/internal/gateway"
	"github.com/finn-freitag/tossbox/internal/grpcserver"
	"github.com/finn-freitag/tossbox/internal/pki"
	"github.com/finn-freitag/tossbox/internal/secrets"
	"github.com/finn-freitag/tossbox/internal/store"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// testDB creates a fresh database and returns its URL.
func testDB(t *testing.T) string {
	base := os.Getenv("TOSSBOX_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TOSSBOX_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("tossbox_it_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err == nil {
			c.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	cfg, _ := pgx.ParseConfig(base)
	return fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable", cfg.User, cfg.Host, cfg.Port, name)
}

type fakeGateway struct {
	mu  sync.Mutex
	tcp []gateway.TCPRoute
}

func (g *fakeGateway) SetRoutes(tcp []gateway.TCPRoute, _ []gateway.HTTPRoute) {
	g.mu.Lock()
	g.tcp = tcp
	g.mu.Unlock()
}

func (g *fakeGateway) routes() []gateway.TCPRoute {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]gateway.TCPRoute(nil), g.tcp...)
}

type env struct {
	t        *testing.T
	api      *httptest.Server
	client   *http.Client
	grpcAddr string
	ca       *pki.CA
	gw       *fakeGateway
	svc      *core.Service
}

func setup(t *testing.T) *env {
	dbURL := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	log := slog.New(slog.DiscardHandler)
	cfg := &config.Config{
		BaseDomain: "servers.example.com", PortRangeStart: 47000, PortRangeEnd: 47099,
		GRPCEndpoint: "localhost:7443", SessionTTL: time.Hour, AdminUsername: "admin", AdminPassword: "adminpass",
		GatewayHTTPListen: ":80", GatewayHTTPSListen: ":443",
	}
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	box, _ := secrets.NewBox(make([]byte, 32))
	ca, err := pki.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(nil)
	cat.Set(catalog.Template{
		ID: "debian-13", Name: "Debian 13", Family: "linux", GuestProfile: "cloud-init", Readiness: "ssh",
		SupportedServices: []string{"ssh", "vnc", "http", "https"},
		MinResources:      catalog.Resources{VCPUs: 1, RAMMB: 512, StorageGB: 8},
		DefaultResources:  catalog.Resources{VCPUs: 2, RAMMB: 2048, StorageGB: 20},
		Images:            map[string]catalog.Image{"libvirt": {URL: "https://example.com/d.qcow2", Format: "qcow2"}},
	}, catalog.Template{
		ID: "win11", Name: "Windows 11", Family: "windows", GuestProfile: "cloudbase-init", License: true,
		SupportedServices: []string{"ssh", "rdp", "vnc"},
		Images:            map[string]catalog.Image{"libvirt": {URL: "local:win11.qcow2", Format: "qcow2"}},
	})
	svc := core.New(core.Deps{Config: cfg, Store: st, Catalog: cat, Box: box, Events: events.NewBroker(), CA: ca, Log: log})
	if _, err := svc.EnsureAdmin(ctx, "admin", "adminpass"); err != nil {
		t.Fatal(err)
	}
	gs, err := grpcserver.New(svc, ca, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetLink(gs)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go gs.Serve(ln)
	t.Cleanup(gs.Stop)
	gw := &fakeGateway{}
	svc.SetGateway(ctx, gw)

	srv := httptest.NewServer(api.New(svc, nil, log).Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &env{t: t, api: srv, client: &http.Client{Jar: jar}, grpcAddr: ln.Addr().String(), ca: ca, gw: gw, svc: svc}
}

// do performs an API request with the session cookie and CSRF header.
func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	return e.doWith(e.client, method, path, body, out, map[string]string{"X-Tossbox-CSRF": "1"})
}

func (e *env) doWith(c *http.Client, method, path string, body any, out any, hdr map[string]string) int {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.api.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode
}

// agent is a minimal fake node agent.
type agent struct {
	t      *testing.T
	key    *ecdsa.PrivateKey
	cert   tls.Certificate
	caPool *x509.CertPool
	stream agentv1.NodeControl_ConnectClient
	conn   *grpc.ClientConn
	in     chan *agentv1.DashboardMessage
}

func enrollAgent(t *testing.T, addr, token string) (*agent, error) {
	tok, err := enroll.ParseToken(token)
	if err != nil {
		return nil, err
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "homelab"}}, key)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		InsecureSkipVerify:    true, // verification is done by the pinned-chain callback
		VerifyPeerCertificate: enroll.VerifyPinnedChain(tok.CAFingerprint),
	})))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := agentv1.NewEnrollmentClient(conn).Enroll(ctx, &agentv1.EnrollRequest{Token: tok.Secret, CsrDer: csr, AgentVersion: "test"})
	if err != nil {
		return nil, err
	}
	caCert, _ := x509.ParseCertificate(res.CaCertDer)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &agent{t: t, key: key, cert: tls.Certificate{Certificate: [][]byte{res.ClientCertDer}, PrivateKey: key}, caPool: pool}, nil
}

func (a *agent) connect(addr string, devices []*agentv1.DeviceStatus) (*agentv1.Welcome, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{a.cert}, RootCAs: a.caPool, ServerName: "localhost",
	})))
	if err != nil {
		return nil, err
	}
	a.conn = conn
	stream, err := agentv1.NewNodeControlClient(conn).Connect(context.Background())
	if err != nil {
		return nil, err
	}
	a.stream = stream
	err = stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
		AgentVersion: "test",
		Capabilities: &agentv1.Capabilities{
			Arch: "x86_64", Os: "linux", HostCores: 8, HostRamMb: 32768, HostStorageGb: 1000,
			Limits: &agentv1.NodeLimits{RamMb: 16384, CpuPercent: 70, StorageGb: 300},
			Drivers: []*agentv1.DriverCapabilities{{Name: "libvirt", Families: []string{"linux", "windows"},
				Services: []string{"ssh", "rdp", "vnc", "http", "https"}, Healthy: true}},
			LicensePools: []*agentv1.LicensePoolInfo{{Name: "win-retail", Type: "retail", Total: 1}},
		},
		Devices: devices,
	}}})
	if err != nil {
		return nil, err
	}
	first, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	a.in = make(chan *agentv1.DashboardMessage, 32)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				close(a.in)
				return
			}
			if m.GetPing() == nil {
				a.in <- m
			}
		}
	}()
	return first.GetWelcome(), nil
}

func (a *agent) next() *agentv1.DashboardMessage {
	a.t.Helper()
	select {
	case m, ok := <-a.in:
		if !ok {
			a.t.Fatal("stream closed")
		}
		return m
	case <-time.After(5 * time.Second):
		a.t.Fatal("timeout waiting for dashboard message")
	}
	return nil
}

func (a *agent) status(id string, st agentv1.DeviceState, ready bool) {
	a.t.Helper()
	if err := a.stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_DeviceStatus{DeviceStatus: &agentv1.DeviceStatus{
		DeviceId: id, State: st, Ready: ready, InternalIp: "192.168.122.10", Message: "ok",
	}}}); err != nil {
		a.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if f() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEndToEnd(t *testing.T) {
	e := setup(t)

	// --- auth
	var me map[string]any
	if code := e.do("GET", "/api/auth/me", nil, nil); code != 401 {
		t.Fatalf("me without login: %d", code)
	}
	if code := e.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong"}, nil); code != 401 {
		t.Fatalf("bad login: %d", code)
	}
	if code := e.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "adminpass"}, &me); code != 200 || me["role"] != "admin" {
		t.Fatalf("login: %d %v", code, me)
	}
	// CSRF header is required for cookie-authenticated writes.
	if code := e.doWith(e.client, "POST", "/api/nodes", map[string]string{"name": "x", "address": "y"}, nil, nil); code != 403 {
		t.Fatalf("csrf: %d", code)
	}

	// --- node + enrollment
	var nodeRes struct {
		Node            struct{ ID, Status string } `json:"node"`
		EnrollmentToken string                      `json:"enrollment_token"`
	}
	if code := e.do("POST", "/api/nodes", map[string]string{"name": "homelab", "address": "127.0.0.1"}, &nodeRes); code != 201 || nodeRes.Node.Status != "pending" {
		t.Fatalf("create node: %d %+v", code, nodeRes)
	}
	ag, err := enrollAgent(t, e.grpcAddr, nodeRes.EnrollmentToken)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if _, err := enrollAgent(t, e.grpcAddr, nodeRes.EnrollmentToken); err == nil {
		t.Fatal("token must be one-time")
	}
	w, err := ag.connect(e.grpcAddr, nil)
	if err != nil || w == nil || w.NodeId != nodeRes.Node.ID || len(w.Devices) != 0 {
		t.Fatalf("connect: %v %v", w, err)
	}
	var node map[string]any
	eventually(t, "node online", func() bool {
		e.do("GET", "/api/nodes/"+nodeRes.Node.ID, nil, &node)
		return node["status"] == "online"
	})

	// --- create options
	var opts struct {
		Templates      []map[string]any    `json:"templates"`
		DriverServices map[string][]string `json:"driver_services"`
		LicensePools   []map[string]any    `json:"license_pools"`
	}
	e.do("GET", "/api/create-options", nil, &opts)
	if len(opts.Templates) != 2 || len(opts.DriverServices["libvirt"]) != 5 || len(opts.LicensePools) != 1 {
		t.Fatalf("create options: %+v", opts)
	}

	// --- create device
	var dev struct {
		ID, Hostname, FQDN, State string
		Services                  []struct {
			Name, Username, Password, Connect string
			Port                              int
		}
	}
	code := e.do("POST", "/api/devices", map[string]any{"name": "My Test VM", "template_id": "debian-13",
		"resources": map[string]any{"vcpus": 2, "ram_mb": 4096, "storage_gb": 20, "cpu_cap_percent": 25}, "ttl_hours": 2}, &dev)
	if code != 201 || dev.Hostname != "my-test-vm" || dev.FQDN != "my-test-vm.servers.example.com" || len(dev.Services) != 1 {
		t.Fatalf("create device: %d %+v", code, dev)
	}
	ssh := dev.Services[0]
	if ssh.Name != "ssh" || ssh.Port != 47000 || len(ssh.Password) < 20 || ssh.Connect != "ssh -p 47000 tossbox@my-test-vm.servers.example.com" {
		t.Fatalf("ssh service: %+v", ssh)
	}
	apply := ag.next().GetApplyDevice().GetDevice()
	if apply.GetDeviceId() != dev.ID || apply.GetServices()[0].GetPublicPort() != 47000 || apply.GetServices()[0].GetPassword() != ssh.Password ||
		apply.GetTemplate().GetImage().GetUrl() != "https://example.com/d.qcow2" || apply.GetResources().GetCpuCapPercent() != 25 {
		t.Fatalf("apply: %v", apply)
	}
	if r := e.gw.routes(); len(r) != 1 || r[0].Port != 47000 || r[0].Target != "127.0.0.1:47000" {
		t.Fatalf("gateway routes: %+v", r)
	}

	// Duplicate hostname -> 409; unsupported service -> 400; reserved -> 409.
	if code := e.do("POST", "/api/devices", map[string]any{"name": "my test vm", "template_id": "debian-13"}, nil); code != 409 {
		t.Fatalf("duplicate hostname: %d", code)
	}
	if code := e.do("POST", "/api/devices", map[string]any{"name": "x1", "template_id": "debian-13", "services": []string{"rdp"}}, nil); code != 400 {
		t.Fatalf("unsupported service: %d", code)
	}
	if code := e.do("POST", "/api/devices", map[string]any{"name": "guacamole", "template_id": "debian-13"}, nil); code != 409 {
		t.Fatalf("reserved: %d", code)
	}
	// Capacity: 16 GB node limit, 4 GB used -> 13 GB doesn't fit.
	if code := e.do("POST", "/api/devices", map[string]any{"name": "big", "template_id": "debian-13", "resources": map[string]any{"ram_mb": 13000}}, nil); code != 503 {
		t.Fatalf("no capacity: %d", code)
	}

	// --- status from agent
	ag.status(dev.ID, agentv1.DeviceState_DEVICE_STATE_RUNNING, true)
	eventually(t, "device running", func() bool {
		var d map[string]any
		e.do("GET", "/api/devices/"+dev.ID, nil, &d)
		return d["state"] == "running" && d["ready"] == true && d["internal_ip"] == "192.168.122.10"
	})

	// --- power off via PATCH -> ApplyDevice with STOPPED
	if code := e.do("PATCH", "/api/devices/"+dev.ID, map[string]any{"power": "stopped"}, nil); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if p := ag.next().GetApplyDevice().GetDevice(); p.GetPower() != agentv1.PowerState_POWER_STATE_STOPPED || p.GetRevision() != 2 {
		t.Fatalf("power apply: %v", p)
	}

	// --- downloads
	resp, _ := e.client.Get(e.api.URL + "/api/devices/" + dev.ID + "/ssh-config")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Port 47000") {
		t.Fatalf("ssh config: %s", b)
	}

	// --- quota for a normal user
	var user map[string]any
	if code := e.do("POST", "/api/users", map[string]any{"username": "bob", "password": "bobpassword", "quota": map[string]any{"max_machines": 1, "allowed_drivers": []string{"libvirt"}}}, &user); code != 201 {
		t.Fatalf("create user: %d", code)
	}
	jar, _ := cookiejar.New(nil)
	bob := &http.Client{Jar: jar}
	csrf := map[string]string{"X-Tossbox-CSRF": "1"}
	if code := e.doWith(bob, "POST", "/api/auth/login", map[string]string{"username": "bob", "password": "bobpassword"}, nil, nil); code != 200 {
		t.Fatalf("bob login: %d", code)
	}
	if code := e.doWith(bob, "POST", "/api/devices", map[string]any{"name": "bob1", "template_id": "debian-13", "resources": map[string]any{"ram_mb": 1024}}, nil, csrf); code != 201 {
		t.Fatalf("bob device 1: %d", code)
	}
	ag.next() // apply for bob1
	if code := e.doWith(bob, "POST", "/api/devices", map[string]any{"name": "bob2", "template_id": "debian-13", "resources": map[string]any{"ram_mb": 1024}}, nil, csrf); code != 422 {
		t.Fatalf("bob quota: %d", code)
	}
	var bobDevs []map[string]any
	e.doWith(bob, "GET", "/api/devices", nil, &bobDevs, nil)
	if len(bobDevs) != 1 {
		t.Fatalf("bob sees %d devices", len(bobDevs))
	}
	if code := e.doWith(bob, "GET", "/api/devices/"+dev.ID, nil, nil, nil); code != 404 {
		t.Fatalf("bob must not see admin device: %d", code)
	}
	if code := e.doWith(bob, "GET", "/api/nodes", nil, nil, nil); code != 403 {
		t.Fatalf("bob nodes: %d", code)
	}
	// License pool not allowed for bob.
	if code := e.doWith(bob, "POST", "/api/devices", map[string]any{"name": "w", "template_id": "win11", "license": map[string]any{"mode": "pool", "pool": "win-retail"}}, nil, csrf); code != 403 {
		t.Fatalf("bob pool: %d", code)
	}

	// --- API token requires TTL
	var tok struct{ Token string }
	e.do("POST", "/api/me/tokens", map[string]any{"name": "ci"}, &tok)
	bearer := map[string]string{"Authorization": "Bearer " + tok.Token}
	if code := e.doWith(http.DefaultClient, "POST", "/api/devices", map[string]any{"name": "ci1", "template_id": "debian-13", "resources": map[string]any{"ram_mb": 512}}, nil, bearer); code != 400 {
		t.Fatalf("api token without ttl: %d", code)
	}
	var ci map[string]any
	if code := e.doWith(http.DefaultClient, "POST", "/api/devices", map[string]any{"name": "ci1", "template_id": "debian-13", "ttl_hours": 1, "resources": map[string]any{"ram_mb": 512}}, &ci, bearer); code != 201 {
		t.Fatalf("api token with ttl: %d %v", code, ci)
	}
	ag.next() // apply for ci1

	// --- reconnect: Welcome has all 3 devices
	ag.conn.Close()
	eventually(t, "node offline", func() bool {
		var d map[string]any
		e.do("GET", "/api/devices/"+dev.ID, nil, &d)
		return d["state"] == "offline"
	})
	w, err = ag.connect(e.grpcAddr, []*agentv1.DeviceStatus{{DeviceId: dev.ID, State: agentv1.DeviceState_DEVICE_STATE_STOPPED}})
	if err != nil || len(w.Devices) != 3 {
		t.Fatalf("reconnect welcome: %v %v", w, err)
	}

	// --- delete: agent gets DeleteDevice, reports DESTROYED, device disappears
	if code := e.do("DELETE", "/api/devices/"+dev.ID, nil, nil); code != 202 {
		t.Fatalf("delete: %d", code)
	}
	if del := ag.next().GetDeleteDevice(); del.GetDeviceId() != dev.ID {
		t.Fatalf("delete msg: %v", del)
	}
	ag.status(dev.ID, agentv1.DeviceState_DEVICE_STATE_DESTROYED, false)
	eventually(t, "device removed", func() bool {
		return e.do("GET", "/api/devices/"+dev.ID, nil, nil) == 404
	})
	for _, r := range e.gw.routes() {
		if r.DeviceID == dev.ID {
			t.Fatal("route of deleted device still present")
		}
	}

	// --- token regeneration revokes the certificate
	var regen struct {
		EnrollmentToken string `json:"enrollment_token"`
	}
	e.do("POST", "/api/nodes/"+nodeRes.Node.ID+"/token", nil, &regen)
	eventually(t, "stream closed after revoke", func() bool {
		select {
		case _, ok := <-ag.in:
			return !ok
		default:
			return false
		}
	})
	if _, err := ag.connect(e.grpcAddr, nil); err == nil {
		t.Fatal("revoked certificate must be rejected")
	}
	ag2, err := enrollAgent(t, e.grpcAddr, regen.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	if w, err := ag2.connect(e.grpcAddr, nil); err != nil || len(w.Devices) != 2 {
		t.Fatalf("re-enrolled connect: %v %v", w, err)
	}

	// --- settings validation
	if code := e.do("PUT", "/api/settings", map[string]any{"password_length": 10}, nil); code != 400 {
		t.Fatalf("settings validation: %d", code)
	}
	var st map[string]any
	if code := e.do("PUT", "/api/settings", map[string]any{"overcommit_ram": true}, &st); code != 200 || st["overcommit_ram"] != true || st["password_length"] != float64(24) {
		t.Fatalf("settings partial update: %d %v", code, st)
	}
}
