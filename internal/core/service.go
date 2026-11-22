// Package core is the application service layer: it ties together the store,
// template catalog, scheduler, gateway, event broker and the node link
// (gRPC). HTTP handlers and the gRPC server only call into this package.
package core

import (
	"context"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/finn-freitag/tb-proto/gen/agent/v1"
	"github.com/finn-freitag/tossbox/internal/catalog"
	"github.com/finn-freitag/tossbox/internal/config"
	"github.com/finn-freitag/tossbox/internal/events"
	"github.com/finn-freitag/tossbox/internal/gateway"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/pki"
	"github.com/finn-freitag/tossbox/internal/ports"
	"github.com/finn-freitag/tossbox/internal/secrets"
	"github.com/finn-freitag/tossbox/internal/store"
)

// NodeLink is the connection to node agents (implemented by grpcserver).
type NodeLink interface {
	// Online reports whether the node has an active control stream.
	Online(nodeID string) bool
	// Send queues a message for the node; false if the node is not connected.
	Send(nodeID string, msg *agentv1.DashboardMessage) bool
	// Disconnect closes the node's stream (e.g. after its certificate was revoked).
	Disconnect(nodeID string)
}

// Gateway is the subset of *gateway.Gateway used by the service.
type Gateway interface {
	SetRoutes(tcp []gateway.TCPRoute, http []gateway.HTTPRoute)
}

// Service is the core application service.
type Service struct {
	cfg     *config.Config
	store   *store.Store
	catalog *catalog.Catalog
	box     *secrets.Box
	events  *events.Broker
	ca      *pki.CA
	log     *slog.Logger
	ports   ports.Allocator

	link NodeLink
	gw   Gateway

	// createMu serializes device creation (scheduling + port allocation +
	// hostname uniqueness) so concurrent requests can't overbook a node.
	createMu sync.Mutex
	// gwMu serializes gateway route rebuilds.
	gwMu sync.Mutex
}

// Deps are the service dependencies.
type Deps struct {
	Config  *config.Config
	Store   *store.Store
	Catalog *catalog.Catalog
	Box     *secrets.Box
	Events  *events.Broker
	CA      *pki.CA
	Log     *slog.Logger
}

// New creates the service. SetLink and SetGateway must be called before use.
func New(d Deps) *Service {
	return &Service{
		cfg: d.Config, store: d.Store, catalog: d.Catalog, box: d.Box, events: d.Events, ca: d.CA,
		log:   d.Log,
		ports: ports.Allocator{Start: d.Config.PortRangeStart, End: d.Config.PortRangeEnd},
		link:  offlineLink{},
	}
}

// SetLink sets the node link.
func (s *Service) SetLink(l NodeLink) { s.link = l }

// SetGateway sets the gateway and installs the current routes.
func (s *Service) SetGateway(ctx context.Context, g Gateway) {
	s.gw = g
	s.SyncGateway(ctx)
}

// Catalog returns the template catalog.
func (s *Service) Catalog() *catalog.Catalog { return s.catalog }

// Config returns the configuration.
func (s *Service) Config() *config.Config { return s.cfg }

// Store returns the store.
func (s *Service) Store() *store.Store { return s.store }

// Events returns the event broker.
func (s *Service) Events() *events.Broker { return s.events }

type offlineLink struct{}

func (offlineLink) Online(string) bool                          { return false }
func (offlineLink) Send(string, *agentv1.DashboardMessage) bool { return false }
func (offlineLink) Disconnect(string)                           {}

// Run starts background loops (TTL expiry, session cleanup) until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ttl := time.NewTicker(30 * time.Second)
	gc := time.NewTicker(time.Hour)
	defer ttl.Stop()
	defer gc.Stop()
	s.expireDevices(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ttl.C:
			s.expireDevices(ctx)
		case <-gc.C:
			if err := s.store.PurgeExpiredSessions(ctx); err != nil {
				s.log.Warn("purge sessions", "err", err)
			}
		}
	}
}

func (s *Service) expireDevices(ctx context.Context) {
	devs, err := s.store.ListExpiredDevices(ctx, time.Now())
	if err != nil {
		s.log.Warn("list expired devices", "err", err)
		return
	}
	for _, d := range devs {
		s.log.Info("device TTL expired, deleting", "device", d.ID, "hostname", d.Hostname)
		if err := s.deleteDevice(ctx, d); err != nil {
			s.log.Warn("delete expired device", "device", d.ID, "err", err)
			continue
		}
		s.store.Audit(ctx, "", "device.expire", d.ID, map[string]any{"hostname": d.Hostname})
	}
}

// SyncGateway rebuilds all gateway routes from the database.
func (s *Service) SyncGateway(ctx context.Context) {
	if s.gw == nil {
		return
	}
	s.gwMu.Lock()
	defer s.gwMu.Unlock()
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		s.log.Error("gateway sync: list nodes", "err", err)
		return
	}
	addr := map[string]string{}
	for _, n := range nodes {
		addr[n.ID] = n.Address
	}
	devs, err := s.store.ListDevices(ctx, "")
	if err != nil {
		s.log.Error("gateway sync: list devices", "err", err)
		return
	}
	var tcp []gateway.TCPRoute
	var http []gateway.HTTPRoute
	for _, d := range devs {
		nodeAddr := addr[d.NodeID]
		if d.Deleting || nodeAddr == "" || d.Exposure == model.ExposureNone {
			continue
		}
		bind := s.cfg.GatewayPublicAddress
		if bind == "" {
			bind = "0.0.0.0"
		}
		if d.Exposure == model.ExposureTailscale {
			if s.cfg.GatewayTailscaleAddress == "" {
				continue
			}
			bind = s.cfg.GatewayTailscaleAddress
		}
		allow := parseAllowlist(d.Allowlist)
		hr := gateway.HTTPRoute{Host: d.Hostname + "." + s.cfg.BaseDomain, Allowlist: allow}
		for _, svc := range d.Services {
			target := joinHostPort(nodeAddr, svc.PublicPort)
			tcp = append(tcp, gateway.TCPRoute{Port: svc.PublicPort, Bind: bind, Target: target, Allowlist: allow, DeviceID: d.ID})
			// Name-based routing on :80/:443 is only for publicly exposed
			// devices (those listeners are bound on all interfaces).
			if d.Exposure == model.ExposurePublic {
				switch svc.Name {
				case "http":
					hr.HTTPTarget = target
				case "https":
					hr.HTTPSTarget = target
				}
			}
		}
		if hr.HTTPTarget != "" || hr.HTTPSTarget != "" {
			http = append(http, hr)
		}
	}
	s.gw.SetRoutes(tcp, http)
}

func joinHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

// parseAllowlist parses IPs and CIDRs, skipping invalid entries (they are
// validated on input).
func parseAllowlist(list []string) []netip.Prefix {
	var out []netip.Prefix
	for _, e := range list {
		if p, err := parsePrefix(e); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func parsePrefix(e string) (netip.Prefix, error) {
	e = strings.TrimSpace(e)
	if strings.Contains(e, "/") {
		p, err := netip.ParsePrefix(e)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(e)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil
}
