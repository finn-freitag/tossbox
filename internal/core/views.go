package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/finn-freitag/tossbox/internal/guac"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/scheduler"
	"github.com/finn-freitag/tossbox/internal/store"
)

// Ref is a reference to another object.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// OwnerRef references the device owner.
type OwnerRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// TemplateRef references the device template.
type TemplateRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Family string `json:"family"`
}

// ServiceView is one exposed service in the API.
type ServiceView struct {
	Name         string `json:"name"`
	Port         int    `json:"port"`
	InternalPort int    `json:"internal_port"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	Connect      string `json:"connect"`
}

// LicenseView shows the license mode (never the key).
type LicenseView struct {
	Mode string `json:"mode"`
	Pool string `json:"pool,omitempty"`
}

// DeviceView is the API representation of a device (docs/API.md).
type DeviceView struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Hostname      string            `json:"hostname"`
	FQDN          string            `json:"fqdn"`
	Owner         OwnerRef          `json:"owner"`
	Node          *Ref              `json:"node"`
	Driver        string            `json:"driver"`
	Template      TemplateRef       `json:"template"`
	Resources     model.Resources   `json:"resources"`
	Power         string            `json:"power"`
	State         string            `json:"state"`
	Ready         bool              `json:"ready"`
	StatusMessage string            `json:"status_message"`
	InternalIP    string            `json:"internal_ip"`
	Exposure      string            `json:"exposure"`
	Allowlist     []string          `json:"allowlist"`
	Services      []ServiceView     `json:"services"`
	License       LicenseView       `json:"license"`
	TTLHours      int               `json:"ttl_hours"`
	ExpiresAt     *time.Time        `json:"expires_at"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Params        map[string]string `json:"params"`
	NetworkPolicy string            `json:"network_policy"`
	GPU           bool              `json:"gpu"`
	// Usage is what the device measurably uses (resources is what it reserves).
	Usage DeviceUsage `json:"usage"`
}

// DeviceUsage is the measured usage of a device, reported by its node
// (refreshed at most every 30 s; RAM and CPU are 0 while it isn't running).
type DeviceUsage struct {
	MemoryUsedMB  int64      `json:"memory_used_mb"`
	StorageUsedMB int64      `json:"storage_used_mb"`
	CPUPercent    float64    `json:"cpu_percent"` // share of the whole node, 0-100
	CPUCores      float64    `json:"cpu_cores"`   // the same as cores in use
	UpdatedAt     *time.Time `json:"updated_at"`
}

type nodeInfo struct {
	name    string
	address string
	online  bool
	cores   int
}

func (s *Service) nodeIndex(ctx context.Context) map[string]nodeInfo {
	out := map[string]nodeInfo{}
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return out
	}
	for _, n := range nodes {
		out[n.ID] = nodeInfo{name: n.Name, address: n.Address, online: s.link.Online(n.ID), cores: parseCaps(n.Capabilities).HostCores}
	}
	return out
}

func (s *Service) view(ctx context.Context, d *store.Device) *DeviceView {
	return s.viewWith(d, s.nodeIndex(ctx))
}

// viewWith builds the view; passwords are included (callers only expose
// devices to their owner and admins).
func (s *Service) viewWith(d *store.Device, nodes map[string]nodeInfo) *DeviceView {
	fqdn := d.Hostname + "." + s.cfg.BaseDomain
	v := &DeviceView{
		ID: d.ID, Name: d.Name, Hostname: d.Hostname, FQDN: fqdn,
		Owner:     OwnerRef{ID: d.OwnerID, Username: d.OwnerName},
		Driver:    d.Driver,
		Template:  TemplateRef{ID: d.TemplateID, Name: d.TemplateName, Family: d.TemplateFamily},
		Resources: d.Res, Power: d.Power, State: d.State, Ready: d.Ready, StatusMessage: d.StatusMessage,
		InternalIP: d.InternalIP, Exposure: d.Exposure, Allowlist: d.Allowlist,
		License:  LicenseView{Mode: d.LicenseMode, Pool: d.LicensePool},
		TTLHours: d.TTLHours, ExpiresAt: d.ExpiresAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		Params: d.Params, Services: []ServiceView{},
		NetworkPolicy: d.NetworkPolicy, GPU: d.GPU,
	}
	if v.License.Mode == "" {
		v.License.Mode = "none"
	}
	v.Usage = DeviceUsage{StorageUsedMB: d.StorageUsedMB, UpdatedAt: d.UsageAt}
	if d.State == "running" {
		v.Usage.MemoryUsedMB, v.Usage.CPUPercent = d.MemoryUsedMB, d.CPUUsedPercent
		if n, ok := nodes[d.NodeID]; ok && n.cores > 0 {
			v.Usage.CPUCores = math.Round(d.CPUUsedPercent*float64(n.cores)) / 100
		}
	}
	if n, ok := nodes[d.NodeID]; ok {
		v.Node = &Ref{ID: d.NodeID, Name: n.name}
		if !n.online && !d.Deleting {
			v.State = "offline"
			v.Ready = false
		}
	}
	password, _ := s.box.Decrypt(d.PasswordEnc)
	// Devices exposed via Tailscale are reached at the gateway's tailscale
	// address (the public DNS name points at the public IP).
	host := fqdn
	if d.Exposure == model.ExposureTailscale && s.cfg.GatewayTailscaleAddress != "" {
		host = s.cfg.GatewayTailscaleAddress
	} else if d.Exposure == model.ExposureNone {
		if n, ok := nodes[d.NodeID]; ok {
			host = n.address
		}
	}
	for _, svc := range d.Services {
		sv := ServiceView{Name: svc.Name, Port: svc.PublicPort, InternalPort: svc.InternalPort}
		if model.CredentialService(d.TemplateFamily, svc.Name) {
			sv.Username, sv.Password = d.Username, password
		}
		hp := host + ":" + strconv.Itoa(svc.PublicPort)
		switch svc.Name {
		case "ssh":
			sv.Connect = fmt.Sprintf("ssh -p %d %s@%s", svc.PublicPort, d.Username, host)
		case "adb":
			sv.Connect = "adb connect " + hp
		case "http":
			sv.Connect = "http://" + hp
			if d.Exposure == model.ExposurePublic && s.cfg.GatewayHTTPListen != "" {
				sv.Connect = "http://" + fqdn
			}
		case "https":
			sv.Connect = "https://" + hp
			if d.Exposure == model.ExposurePublic && s.cfg.GatewayHTTPSListen != "" {
				sv.Connect = "https://" + fqdn
			}
		default:
			sv.Connect = hp
		}
		v.Services = append(v.Services, sv)
	}
	return v
}

// publishDevice builds the view, publishes it on the event bus and returns it.
func (s *Service) publishDevice(ctx context.Context, d *store.Device) *DeviceView {
	v := s.view(ctx, d)
	s.events.Publish("device", v, d.OwnerID, false)
	return v
}

// RDPFile returns a .rdp file for the device.
func (s *Service) RDPFile(ctx context.Context, p Principal, id string) (string, string, error) {
	d, err := s.GetDevice(ctx, p, id)
	if err != nil {
		return "", "", err
	}
	v := s.view(ctx, d)
	for _, svc := range v.Services {
		if svc.Name == "rdp" {
			addr := svc.Connect
			body := strings.Join([]string{
				"full address:s:" + addr,
				"username:s:" + d.Username,
				"prompt for credentials:i:1",
				"authentication level:i:2",
				"screen mode id:i:2",
				"session bpp:i:32",
				"",
			}, "\r\n")
			return d.Hostname + ".rdp", body, nil
		}
	}
	return "", "", model.Errf(model.CodeNotFound, "device has no RDP service")
}

// SSHConfig returns an OpenSSH config snippet for the device.
func (s *Service) SSHConfig(ctx context.Context, p Principal, id string) (string, error) {
	d, err := s.GetDevice(ctx, p, id)
	if err != nil {
		return "", err
	}
	svc := d.Service("ssh")
	if svc == nil {
		return "", model.Errf(model.CodeNotFound, "device has no SSH service")
	}
	v := s.view(ctx, d)
	host := v.FQDN
	if d.Exposure == model.ExposureTailscale && s.cfg.GatewayTailscaleAddress != "" {
		host = s.cfg.GatewayTailscaleAddress
	}
	return fmt.Sprintf("Host %s\n    HostName %s\n    Port %d\n    User %s\n", d.Hostname, host, svc.PublicPort, d.Username), nil
}

// guacProtocols are the services Guacamole can open, in order of preference.
var guacProtocols = []string{"rdp", "vnc", "ssh"}

func guacConnectionName(d *store.Device, service string) string {
	return d.Hostname + " (" + service + ")"
}

// guacTargets returns the Guacamole connections of a device.
func (s *Service) guacTargets(d *store.Device, nodes map[string]string) []guac.Target {
	addr, ok := nodes[d.NodeID]
	if !ok || d.Deleting {
		return nil
	}
	pw, err := s.box.Decrypt(d.PasswordEnc)
	if err != nil {
		return nil
	}
	var out []guac.Target
	for _, svc := range guacProtocols {
		if sv := d.Service(svc); sv != nil {
			// guacd runs next to the dashboard and reaches the node directly.
			out = append(out, guac.Target{
				Name: guacConnectionName(d, svc), Protocol: svc,
				Host: addr, Port: sv.PublicPort, Username: d.Username, Password: pw,
			})
		}
	}
	return out
}

// GuacamoleURL returns a short-lived login URL for Guacamole that opens the
// device's connection. The login also carries the caller's other devices'
// connections (see guac.Login for why).
func (s *Service) GuacamoleURL(ctx context.Context, p Principal, id, service string) (string, error) {
	if !s.cfg.GuacamoleEnabled() {
		return "", model.Errf(model.CodeNotFound, "guacamole is not configured")
	}
	d, err := s.GetDevice(ctx, p, id)
	if err != nil {
		return "", err
	}
	if service == "" {
		for _, c := range guacProtocols {
			if d.Service(c) != nil {
				service = c
				break
			}
		}
	}
	if d.Service(service) == nil || !model.CredentialServices[service] {
		return "", model.Errf(model.CodeNotFound, "device has no %s service usable with Guacamole", service)
	}
	nodeList, err := s.store.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	nodes := make(map[string]string, len(nodeList))
	for _, n := range nodeList {
		nodes[n.ID] = n.Address
	}
	targets := s.guacTargets(d, nodes)
	if own, err := s.store.ListDevices(ctx, p.User.ID); err == nil {
		for _, o := range own {
			if o.ID != d.ID {
				targets = append(targets, s.guacTargets(o, nodes)...)
			}
		}
	}
	return guac.LoginURL(s.cfg.GuacamoleURL, s.cfg.GuacamoleJSONSecret, p.User.Username, targets,
		guacConnectionName(d, service), 5*time.Minute, s.cfg.GuacamoleLoginHelper)
}

// ---------------------------------------------------------------- node snapshots

func parseCaps(raw json.RawMessage) model.NodeCapabilities {
	var c model.NodeCapabilities
	_ = json.Unmarshal(raw, &c)
	if c.Drivers == nil {
		c.Drivers = []model.DriverInfo{}
	}
	if c.LicensePools == nil {
		c.LicensePools = []model.LicensePoolInfo{}
	}
	return c
}

func parseUsage(raw json.RawMessage) model.NodeUsage {
	var u model.NodeUsage
	_ = json.Unmarshal(raw, &u)
	return u
}

// nodeSnapshots builds scheduler inputs for all nodes.
// The store nodes are returned too, index-aligned with the snapshots.
func (s *Service) nodeSnapshots(ctx context.Context) ([]scheduler.Node, []*store.Node, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	devs, err := s.store.ListDevices(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	byNode := map[string][]scheduler.DeviceLoad{}
	for _, d := range devs {
		if d.NodeID == "" {
			continue
		}
		// Deleting devices still occupy resources until the node destroyed them.
		byNode[d.NodeID] = append(byNode[d.NodeID], scheduler.DeviceLoad{
			Driver: d.Driver, Res: d.Res, Stopped: d.Power == model.PowerStopped, LicensePool: d.LicensePool, GPU: d.GPU,
		})
	}
	out := make([]scheduler.Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, snapshot(n, byNode[n.ID], s.link.Online(n.ID)))
	}
	return out, nodes, nil
}

func snapshot(n *store.Node, devs []scheduler.DeviceLoad, online bool) scheduler.Node {
	c := parseCaps(n.Capabilities)
	u := parseUsage(n.Usage)
	sn := scheduler.Node{
		ID: n.ID, Name: n.Name, Online: online && n.Enrolled, Arch: c.Arch,
		HostCores: c.HostCores, HostRAMMB: c.HostRAMMB, HostStorageGB: c.HostStorageGB,
		Limits:        scheduler.Limits{RAMMB: c.Limits.RAMMB, CPUPercent: c.Limits.CPUPercent, StorageGB: c.Limits.StorageGB},
		Drivers:       map[string]scheduler.DriverCaps{},
		LicensePools:  map[string]int{},
		StorageUsedGB: u.StorageUsedGB,
		Devices:       devs,
		LocalImages:   c.LocalImages,
	}
	for _, d := range c.Drivers {
		sn.Drivers[d.Name] = scheduler.DriverCaps{Families: d.Families, Services: d.Services, FixedSlots: d.FixedSlots, Healthy: d.Healthy, Message: d.Message,
			NetworkPolicies: d.NetworkPolicies, GPUMode: d.GPUMode, GPUSlots: d.GPUSlots, GPUFamilies: d.GPUFamilies}
	}
	for _, p := range c.LicensePools {
		sn.LicensePools[p.Name] += p.Total
	}
	return sn
}
