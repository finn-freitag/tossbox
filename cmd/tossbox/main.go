// Command tossbox runs the Tossbox dashboard: REST API + web UI, the gRPC
// endpoint for node agents and the traffic gateway.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	"github.com/finn-freitag/tossbox/internal/version"
	"github.com/finn-freitag/tossbox/internal/webui"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(version.Version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tossbox:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	log.Info("starting tossbox", "version", version.Version, "base_domain", cfg.BaseDomain)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	key, created, err := secrets.LoadOrCreateKey(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}
	if created {
		log.Warn("TOSSBOX_SECRET_KEY not set: generated a key in the data dir; back it up, losing it makes stored passwords unreadable")
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return err
	}
	ca, err := pki.LoadOrCreateCA(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("CA: %w", err)
	}

	st, err := openStore(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer st.Close()

	cat := catalog.New(cfg.TemplateSources)
	broker := events.NewBroker()
	svc := core.New(core.Deps{Config: cfg, Store: st, Catalog: cat, Box: box, Events: broker, CA: ca, Log: log})

	if createdAdmin, err := svc.EnsureAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return err
	} else if createdAdmin {
		log.Info("created initial admin user", "username", cfg.AdminUsername)
	}
	n, errs := svc.ReloadTemplates(ctx)
	log.Info("templates loaded", "count", n, "errors", len(errs))

	// gRPC for node agents.
	hosts := cfg.GRPCHosts
	if cfg.DashboardHost != "" {
		hosts = append(hosts, cfg.DashboardHost)
	}
	if h, _, err := net.SplitHostPort(cfg.GRPCEndpoint); err == nil {
		hosts = append(hosts, h)
	}
	gs, err := grpcserver.New(svc, ca, hosts, log)
	if err != nil {
		return err
	}
	svc.SetLink(gs)
	gln, err := net.Listen("tcp", cfg.GRPCListen)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	go func() {
		if err := gs.Serve(gln); err != nil {
			log.Error("grpc server", "err", err)
			stop()
		}
	}()
	log.Info("agent endpoint listening", "addr", cfg.GRPCListen, "endpoint", cfg.GRPCEndpoint, "ca_fingerprint", ca.Fingerprint())

	// REST API + UI.
	handler := api.New(svc, webui.Handler(), log).Handler()
	httpSrv := &http.Server{Addr: cfg.HTTPListen, Handler: handler, ReadHeaderTimeout: 15 * time.Second}
	hln, err := net.Listen("tcp", cfg.HTTPListen)
	if err != nil {
		return fmt.Errorf("http listen: %w", err)
	}
	go func() {
		if err := httpSrv.Serve(hln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()
	log.Info("dashboard listening", "addr", cfg.HTTPListen)

	// Gateway.
	gwCfg := gateway.Config{
		HTTPListen: cfg.GatewayHTTPListen, HTTPSListen: cfg.GatewayHTTPSListen,
		FallbackHTTP: cfg.GatewayFallbackHTTP, FallbackHTTPS: cfg.GatewayFallbackHTTPS,
		RateLimit:     cfg.GatewayRateLimit,
		DashboardHost: cfg.DashboardHost, DashboardHandler: handler,
	}
	if cfg.GuacamoleEnabled() {
		gwCfg.GuacamoleHost = cfg.GuacamoleHost()
		gwCfg.GuacamoleUpstream = cfg.GuacamoleUpstream
		if u, err := url.Parse(cfg.GuacamoleURL); err == nil {
			gwCfg.GuacamolePath = u.Path
		}
	}
	if cfg.ACMEEmail != "" {
		var hostsACME []string
		if cfg.DashboardHost != "" {
			hostsACME = append(hostsACME, cfg.DashboardHost)
		}
		if gwCfg.GuacamoleHost != "" {
			hostsACME = append(hostsACME, gwCfg.GuacamoleHost)
		}
		if len(hostsACME) > 0 {
			m := &autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				Email:      cfg.ACMEEmail,
				Cache:      autocert.DirCache(cfg.ACMECacheDir),
				HostPolicy: autocert.HostWhitelist(hostsACME...),
			}
			if cfg.ACMEDirURL != "" {
				m.Client = &acme.Client{DirectoryURL: cfg.ACMEDirURL}
			}
			gwCfg.ACME = m
			log.Info("ACME enabled", "hosts", hostsACME)
		}
	}
	gw := gateway.New(gwCfg, log)
	if err := gw.Start(); err != nil {
		return fmt.Errorf("gateway: %w (ports 80/443 need CAP_NET_BIND_SERVICE; set TOSSBOX_GATEWAY_HTTP_LISTEN/HTTPS_LISTEN to empty to disable)", err)
	}
	svc.SetGateway(ctx, gw)

	go svc.Run(ctx)

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutCtx)
	gw.Shutdown(shutCtx)
	gs.Stop()
	return nil
}

// openStore connects to the database, retrying for a while so the dashboard
// can start together with its database container.
func openStore(ctx context.Context, url string, log *slog.Logger) (*store.Store, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		st, err := store.Open(ctx, url)
		if err == nil {
			return st, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("database: %w", err)
		}
		log.Warn("database not ready, retrying", "err", err)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(cfg.LogLevel))
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
