package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	agentv1 "github.com/finn-freitag/tb-proto/gen/agent/v1"
	"github.com/finn-freitag/tossbox/internal/catalog"
	"github.com/finn-freitag/tossbox/internal/hostname"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/scheduler"
	"github.com/finn-freitag/tossbox/internal/secrets"
	"github.com/finn-freitag/tossbox/internal/store"
	"github.com/google/uuid"
)

// Principal is the authenticated caller.
type Principal struct {
	User     *store.User
	ViaToken bool // authenticated with an API token (automation)
}

// IsAdmin reports whether the caller is an admin.
func (p Principal) IsAdmin() bool { return p.User != nil && p.User.Role == model.RoleAdmin }

// CreateDeviceRequest is the body of POST /api/devices.
type CreateDeviceRequest struct {
	Name       string            `json:"name"`
	TemplateID string            `json:"template_id"`
	Driver     string            `json:"driver"`
	NodeID     string            `json:"node_id"`
	Resources  *model.Resources  `json:"resources"`
	Services   *[]string         `json:"services"`
	Exposure   string            `json:"exposure"`
	Allowlist  []string          `json:"allowlist"`
	TTLHours   int               `json:"ttl_hours"`
	License    *LicenseRequest   `json:"license"`
	Power      string            `json:"power"`
	Params     map[string]string `json:"params"`
}

// LicenseRequest selects the Windows license option.
type LicenseRequest struct {
	Mode    string `json:"mode"` // none | user_key | pool
	Pool    string `json:"pool"`
	UserKey string `json:"user_key"`
}

// templateSnapshot is stored with each device and becomes TemplateSpec.
type templateSnapshot struct {
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Family       string            `json:"family"`
	GuestProfile string            `json:"guest_profile"`
	Readiness    string            `json:"readiness"`
	DefaultUser  string            `json:"default_user"`
	Image        catalog.Image     `json:"image"`
	Params       map[string]string `json:"params"`
	Icon         string            `json:"icon,omitempty"`
}

// DefaultServicesFor returns the default services of a template: the global
// default services (+ adb, which is always on for Android) that the template supports.
func DefaultServicesFor(t *catalog.Template, st model.Settings) []string {
	out := []string{}
	for _, svc := range model.Services {
		def := slices.Contains(st.DefaultServices, svc) || svc == "adb"
		if def && t.Supports(svc) {
			out = append(out, svc)
		}
	}
	return out
}

// CreateDevice validates, schedules and creates a device.
func (s *Service) CreateDevice(ctx context.Context, p Principal, req CreateDeviceRequest) (*DeviceView, error) {
	u := p.User
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}

	// --- validation
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		return nil, model.Errf(model.CodeValidation, "name is required (max 100 characters)")
	}
	host, err := hostname.Slug(req.Name)
	if err != nil {
		return nil, model.Errf(model.CodeValidation, "%v", err)
	}
	if hostname.IsReserved(host, s.reservedLabel()) {
		return nil, model.Errf(model.CodeConflict, "hostname %q is reserved", host)
	}
	tpl, ok := s.catalog.Get(req.TemplateID)
	if !ok {
		return nil, model.Errf(model.CodeValidation, "unknown template %q", req.TemplateID)
	}
	if !model.Allows(u.Quota.AllowedTemplates, tpl.ID) {
		return nil, model.Errf(model.CodeForbidden, "you may not use template %s", tpl.ID)
	}
	var drivers []string
	if req.Driver != "" {
		if _, ok := tpl.Images[req.Driver]; !ok {
			return nil, model.Errf(model.CodeValidation, "template %s doesn't support driver %s", tpl.ID, req.Driver)
		}
		if !model.Allows(u.Quota.AllowedDrivers, req.Driver) {
			return nil, model.Errf(model.CodeForbidden, "you may not use driver %s", req.Driver)
		}
		drivers = []string{req.Driver}
	} else {
		for _, d := range tpl.Drivers() {
			if model.Allows(u.Quota.AllowedDrivers, d) {
				drivers = append(drivers, d)
			}
		}
		if len(drivers) == 0 {
			return nil, model.Errf(model.CodeForbidden, "none of the drivers of template %s is allowed for you", tpl.ID)
		}
	}

	res := tpl.DefaultResources
	if req.Resources != nil {
		r := *req.Resources
		if r.VCPUs > 0 {
			res.VCPUs = r.VCPUs
		}
		if r.RAMMB > 0 {
			res.RAMMB = r.RAMMB
		}
		if r.StorageGB > 0 {
			res.StorageGB = r.StorageGB
		}
		res.CPUCapPercent = r.CPUCapPercent
	}
	resources := model.Resources{VCPUs: res.VCPUs, CPUCapPercent: res.CPUCapPercent, RAMMB: res.RAMMB, StorageGB: res.StorageGB}
	if resources.VCPUs < tpl.MinResources.VCPUs || resources.RAMMB < tpl.MinResources.RAMMB || resources.StorageGB < tpl.MinResources.StorageGB {
		return nil, model.Errf(model.CodeValidation, "resources below template minimum (%d vCPU, %d MB RAM, %d GB storage)",
			tpl.MinResources.VCPUs, tpl.MinResources.RAMMB, tpl.MinResources.StorageGB)
	}
	if resources.CPUCapPercent < 0 || resources.CPUCapPercent > 100 {
		return nil, model.Errf(model.CodeValidation, "cpu_cap_percent must be between 0 and 100")
	}

	var services []string
	if req.Services == nil {
		services = DefaultServicesFor(tpl, st)
	} else {
		for _, svc := range *req.Services {
			if !model.IsService(svc) {
				return nil, model.Errf(model.CodeValidation, "unknown service %q", svc)
			}
			if !tpl.Supports(svc) {
				return nil, model.Errf(model.CodeValidation, "template %s doesn't support service %s", tpl.ID, svc)
			}
			if !slices.Contains(services, svc) {
				services = append(services, svc)
			}
		}
	}

	if req.Exposure == "" {
		req.Exposure = model.ExposurePublic
	}
	if !slices.Contains(model.Exposures, req.Exposure) {
		return nil, model.Errf(model.CodeValidation, "exposure must be public, tailscale or none")
	}
	if !model.Allows(u.Quota.AllowedExposures, req.Exposure) {
		return nil, model.Errf(model.CodeForbidden, "you may not use exposure %s", req.Exposure)
	}
	if req.Exposure == model.ExposureTailscale && s.cfg.GatewayTailscaleAddress == "" {
		return nil, model.Errf(model.CodeValidation, "tailscale exposure is not configured on this dashboard (TOSSBOX_GATEWAY_TAILSCALE_ADDRESS)")
	}
	if req.Exposure == model.ExposurePublic && slices.Contains(services, "vnc") && !st.PublicVNCAllowed {
		return nil, model.Errf(model.CodeForbidden, "public VNC is disabled by the administrator; use tailscale exposure or Guacamole")
	}
	if req.Exposure == model.ExposurePublic && slices.Contains(services, "adb") && !st.PublicADBAllowed {
		return nil, model.Errf(model.CodeForbidden, "public ADB is disabled by the administrator (adb has no authentication); use tailscale or none exposure")
	}
	allowlist, err := normalizeAllowlist(req.Allowlist)
	if err != nil {
		return nil, err
	}

	if req.TTLHours < 0 {
		return nil, model.Errf(model.CodeValidation, "ttl_hours must be >= 0")
	}
	if req.TTLHours == 0 {
		req.TTLHours = st.DefaultTTLHours
	}
	if p.ViaToken && st.APIRequiresTTL && req.TTLHours == 0 {
		return nil, model.Errf(model.CodeValidation, "devices created via API token need ttl_hours > 0")
	}

	lic := LicenseRequest{Mode: "none"}
	if req.License != nil && req.License.Mode != "" {
		lic = *req.License
	}
	licensePool, licenseKey := "", ""
	switch lic.Mode {
	case "none":
	case "user_key", "pool":
		if !tpl.License {
			return nil, model.Errf(model.CodeValidation, "template %s has no license options", tpl.ID)
		}
		if lic.Mode == "user_key" {
			licenseKey = strings.TrimSpace(lic.UserKey)
			if len(licenseKey) < 5 {
				return nil, model.Errf(model.CodeValidation, "license key is required")
			}
		} else {
			if lic.Pool == "" {
				return nil, model.Errf(model.CodeValidation, "license pool is required")
			}
			if !slices.Contains(u.Quota.AllowedLicensePools, lic.Pool) && !p.IsAdmin() {
				return nil, model.Errf(model.CodeForbidden, "you may not use license pool %s", lic.Pool)
			}
			licensePool = lic.Pool
		}
	default:
		return nil, model.Errf(model.CodeValidation, "license mode must be none, user_key or pool")
	}

	if req.Power == "" {
		req.Power = model.PowerRunning
	}
	if req.Power != model.PowerRunning && req.Power != model.PowerStopped {
		return nil, model.Errf(model.CodeValidation, "power must be running or stopped")
	}

	// --- quota, scheduling, allocation (serialized)
	s.createMu.Lock()
	defer s.createMu.Unlock()

	usage, err := s.Usage(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if err := scheduler.CheckQuota(u.Quota, usage, scheduler.QuotaRequest{Res: resources, TTLHours: req.TTLHours}); err != nil {
		return nil, err
	}
	snaps, _, err := s.nodeSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	// A service must be supported by the template AND the driver: reject
	// services no online node offers for any candidate driver as invalid
	// (instead of a generic no_capacity from the scheduler).
	if offered := driverServices(snaps, drivers); len(offered) > 0 {
		for _, svc := range services {
			if !offered[svc] {
				return nil, model.Errf(model.CodeValidation, "service %s is not supported by driver(s) %s", svc, strings.Join(drivers, ", "))
			}
		}
	}
	place, err := scheduler.Pick(snaps, scheduler.Request{
		Family: tpl.Family, Arch: tpl.Arch, Drivers: drivers, NodeID: req.NodeID,
		Res: resources, Services: services, LicensePool: licensePool,
	}, st)
	if err != nil {
		return nil, err
	}
	if place.Driver == "usb-phone" {
		if err := scheduler.CheckQuota(u.Quota, usage, scheduler.QuotaRequest{Phone: true, TTLHours: req.TTLHours}); err != nil {
			return nil, err
		}
	}
	if taken, err := s.store.HostnameTaken(ctx, host); err != nil {
		return nil, err
	} else if taken {
		return nil, model.Errf(model.CodeConflict, "hostname %q is already in use, choose another name", host)
	}
	used, err := s.store.UsedPorts(ctx)
	if err != nil {
		return nil, err
	}
	pts, err := s.ports.Allocate(used, len(services))
	if err != nil {
		return nil, model.Errf(model.CodeNoCapacity, "no free ports left in the port range")
	}

	password, err := secrets.Password(st.PasswordLength)
	if err != nil {
		return nil, err
	}
	pwEnc, err := s.box.Encrypt(password)
	if err != nil {
		return nil, err
	}
	keyEnc, err := s.box.Encrypt(licenseKey)
	if err != nil {
		return nil, err
	}
	img := tpl.Images[place.Driver]
	snap, _ := json.Marshal(templateSnapshot{
		ID: tpl.ID, Version: tpl.Version, Family: tpl.Family, GuestProfile: tpl.GuestProfile,
		Readiness: tpl.Readiness, DefaultUser: tpl.DefaultUser, Image: img, Params: tpl.Params, Icon: tpl.Icon,
	})
	var expires *time.Time
	if req.TTLHours > 0 {
		t := time.Now().Add(time.Duration(req.TTLHours) * time.Hour).UTC()
		expires = &t
	}
	d := &store.Device{
		ID: uuid.NewString(), OwnerID: u.ID, NodeID: place.NodeID, Driver: place.Driver,
		TemplateID: tpl.ID, TemplateName: tpl.Name, TemplateFamily: tpl.Family, TemplateSpec: snap,
		Name: req.Name, Hostname: host, Res: resources, Power: req.Power, State: "pending",
		StatusMessage: "Scheduled", Exposure: req.Exposure, Allowlist: allowlist,
		LicenseMode: lic.Mode, LicensePool: licensePool, LicenseKeyEnc: keyEnc,
		Username: tpl.DefaultUser, PasswordEnc: pwEnc, SSHKeys: u.SSHKeys, ADBKeys: u.ADBKeys, Params: req.Params,
		TTLHours: req.TTLHours, ExpiresAt: expires, Revision: 1,
	}
	for i, svc := range services {
		d.Services = append(d.Services, store.Service{Name: svc, InternalPort: tpl.ServicePort(svc), PublicPort: pts[i]})
	}
	if !s.link.Online(place.NodeID) {
		d.StatusMessage = "Waiting for node to connect"
	}
	if err := s.store.InsertDevice(ctx, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, model.Errf(model.CodeConflict, "hostname or port conflict, please retry")
		}
		return nil, err
	}
	d, err = s.store.GetDevice(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	s.pushDevice(d)
	s.SyncGateway(ctx)
	s.store.Audit(ctx, u.ID, "device.create", d.ID, map[string]any{"hostname": d.Hostname, "template": tpl.ID, "node": place.NodeID, "driver": place.Driver})
	s.log.Info("device created", "device", d.ID, "hostname", host, "node", place.NodeID, "driver", place.Driver)
	return s.publishDevice(ctx, d), nil
}

// driverServices returns the union of services that online, healthy nodes
// offer for the given drivers.
func driverServices(nodes []scheduler.Node, drivers []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range nodes {
		if !n.Online {
			continue
		}
		for _, drv := range drivers {
			if c, ok := n.Drivers[drv]; ok && c.Healthy {
				for _, svc := range c.Services {
					out[svc] = true
				}
			}
		}
	}
	return out
}

func (s *Service) reservedLabel() string {
	if h := s.cfg.DashboardHost; strings.HasSuffix(h, "."+s.cfg.BaseDomain) {
		return strings.TrimSuffix(h, "."+s.cfg.BaseDomain)
	}
	return ""
}

func normalizeAllowlist(in []string) ([]string, error) {
	out := []string{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		p, err := parsePrefix(e)
		if err != nil {
			return nil, model.Errf(model.CodeValidation, "invalid allowlist entry %q (IP or CIDR expected)", e)
		}
		out = append(out, p.String())
	}
	return out, nil
}

// Usage computes a user's resource usage over their devices.
func (s *Service) Usage(ctx context.Context, userID string) (model.Usage, error) {
	devs, err := s.store.ListDevices(ctx, userID)
	if err != nil {
		return model.Usage{}, err
	}
	return usageOf(devs), nil
}

func usageOf(devs []*store.Device) model.Usage {
	var u model.Usage
	for _, d := range devs {
		if d.Deleting {
			continue
		}
		u.Machines++
		u.VCPUs += d.Res.VCPUs
		u.RAMMB += d.Res.RAMMB
		u.StorageGB += d.Res.StorageGB
		if d.Driver == "usb-phone" {
			u.Phones++
		}
	}
	return u
}

// GetDevice returns a device the principal may see.
func (s *Service) GetDevice(ctx context.Context, p Principal, id string) (*store.Device, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, model.Errf(model.CodeNotFound, "device not found")
	}
	d, err := s.store.GetDevice(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !p.IsAdmin() && d.OwnerID != p.User.ID) {
		return nil, model.Errf(model.CodeNotFound, "device not found")
	}
	return d, err
}

// DeviceView returns the API view of a device.
func (s *Service) DeviceView(ctx context.Context, p Principal, id string) (*DeviceView, error) {
	d, err := s.GetDevice(ctx, p, id)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, d), nil
}

// ListDevices returns devices visible to the principal. Admins see all
// devices unless all=false; owner filters by user.
func (s *Service) ListDevices(ctx context.Context, p Principal, owner string, all bool) ([]*DeviceView, error) {
	filter := p.User.ID
	if p.IsAdmin() {
		filter = owner
		if owner == "" && !all {
			filter = p.User.ID
		}
	}
	devs, err := s.store.ListDevices(ctx, filter)
	if err != nil {
		return nil, err
	}
	nodes := s.nodeIndex(ctx)
	out := make([]*DeviceView, 0, len(devs))
	for _, d := range devs {
		out = append(out, s.viewWith(d, nodes))
	}
	return out, nil
}

// UpdateDeviceRequest is the body of PATCH /api/devices/{id}.
type UpdateDeviceRequest struct {
	Power     *string   `json:"power"`
	Name      *string   `json:"name"`
	TTLHours  *int      `json:"ttl_hours"`
	Allowlist *[]string `json:"allowlist"`
}

// UpdateDevice applies a PATCH.
func (s *Service) UpdateDevice(ctx context.Context, p Principal, id string, req UpdateDeviceRequest) (*DeviceView, error) {
	d, err := s.GetDevice(ctx, p, id)
	if err != nil {
		return nil, err
	}
	if d.Deleting {
		return nil, model.Errf(model.CodeConflict, "device is being deleted")
	}
	var up store.DeviceUpdate
	if req.Power != nil {
		if *req.Power != model.PowerRunning && *req.Power != model.PowerStopped {
			return nil, model.Errf(model.CodeValidation, "power must be running or stopped")
		}
		up.Power = req.Power
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" || len(n) > 100 {
			return nil, model.Errf(model.CodeValidation, "name is required (max 100 characters)")
		}
		up.Name = &n
	}
	if req.TTLHours != nil {
		ttl := *req.TTLHours
		if ttl < 0 {
			return nil, model.Errf(model.CodeValidation, "ttl_hours must be >= 0")
		}
		owner, err := s.store.GetUser(ctx, d.OwnerID)
		if err != nil {
			return nil, err
		}
		if q := owner.Quota.MaxTTLHours; q > 0 && (ttl == 0 || ttl > q) && !p.IsAdmin() {
			return nil, model.Errf(model.CodeQuotaExceeded, "ttl must be between 1 and %d hours", q)
		}
		// The TTL counts from creation (API.md): expires_at = created_at + ttl.
		var exp *time.Time
		if ttl > 0 {
			t := d.CreatedAt.Add(time.Duration(ttl) * time.Hour).UTC()
			exp = &t
		}
		up.TTLHours, up.ExpiresAt = &ttl, &exp
	}
	if req.Allowlist != nil {
		al, err := normalizeAllowlist(*req.Allowlist)
		if err != nil {
			return nil, err
		}
		up.Allowlist = &al
	}
	if err := s.store.UpdateDevice(ctx, d.ID, up); err != nil {
		return nil, err
	}
	d, err = s.store.GetDevice(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	if req.Power != nil {
		s.pushDevice(d)
		s.store.Audit(ctx, p.User.ID, "device.power", d.ID, map[string]any{"power": d.Power})
	}
	if req.Allowlist != nil {
		s.SyncGateway(ctx)
	}
	return s.publishDevice(ctx, d), nil
}

// DeleteDevice starts deleting a device.
func (s *Service) DeleteDevice(ctx context.Context, p Principal, id string) error {
	d, err := s.GetDevice(ctx, p, id)
	if err != nil {
		return err
	}
	if err := s.deleteDevice(ctx, d); err != nil {
		return err
	}
	s.store.Audit(ctx, p.User.ID, "device.delete", d.ID, map[string]any{"hostname": d.Hostname})
	return nil
}

func (s *Service) deleteDevice(ctx context.Context, d *store.Device) error {
	// A device on a node that never enrolled can't exist anywhere: drop it now.
	if d.NodeID == "" {
		return s.removeDevice(ctx, d)
	}
	n, err := s.store.GetNode(ctx, d.NodeID)
	if err == nil && !n.Enrolled && n.AgentVersion == "" {
		return s.removeDevice(ctx, d)
	}
	if err := s.store.MarkDeleting(ctx, d.ID); err != nil {
		return err
	}
	s.link.Send(d.NodeID, &agentv1.DashboardMessage{Msg: &agentv1.DashboardMessage_DeleteDevice{DeleteDevice: &agentv1.DeleteDevice{DeviceId: d.ID}}})
	s.SyncGateway(ctx)
	if d2, err := s.store.GetDevice(ctx, d.ID); err == nil {
		s.publishDevice(ctx, d2)
	}
	return nil
}

// removeDevice deletes the row (freeing ports and lease) and notifies clients.
func (s *Service) removeDevice(ctx context.Context, d *store.Device) error {
	if err := s.store.DeleteDevice(ctx, d.ID); err != nil {
		return err
	}
	s.events.Publish("device_deleted", map[string]string{"id": d.ID}, d.OwnerID, false)
	s.SyncGateway(ctx)
	s.log.Info("device removed", "device", d.ID, "hostname", d.Hostname)
	return nil
}

// pushDevice sends the device's desired state to its node (if connected).
func (s *Service) pushDevice(d *store.Device) {
	if d.NodeID == "" {
		return
	}
	spec, err := s.spec(d)
	if err != nil {
		s.log.Error("build device spec", "device", d.ID, "err", err)
		return
	}
	s.link.Send(d.NodeID, &agentv1.DashboardMessage{Msg: &agentv1.DashboardMessage_ApplyDevice{ApplyDevice: &agentv1.ApplyDevice{Device: spec}}})
}

// spec builds the agent DeviceSpec for a device.
func (s *Service) spec(d *store.Device) (*agentv1.DeviceSpec, error) {
	var ts templateSnapshot
	if err := json.Unmarshal(d.TemplateSpec, &ts); err != nil {
		return nil, fmt.Errorf("template snapshot: %w", err)
	}
	password, err := s.box.Decrypt(d.PasswordEnc)
	if err != nil {
		return nil, err
	}
	key, err := s.box.Decrypt(d.LicenseKeyEnc)
	if err != nil {
		return nil, err
	}
	spec := &agentv1.DeviceSpec{
		DeviceId: d.ID,
		Hostname: d.Hostname,
		Driver:   d.Driver,
		Template: &agentv1.TemplateSpec{
			Id: ts.ID, Version: ts.Version, Family: ts.Family, GuestProfile: ts.GuestProfile,
			Readiness: ts.Readiness, DefaultUser: ts.DefaultUser,
			Image:  &agentv1.ImageSpec{Url: ts.Image.URL, Format: ts.Image.Format, Sha256: ts.Image.SHA256},
			Params: ts.Params,
		},
		Resources: &agentv1.Resources{
			Vcpus: int32(d.Res.VCPUs), CpuCapPercent: int32(d.Res.CPUCapPercent),
			RamMb: d.Res.RAMMB, StorageGb: d.Res.StorageGB,
		},
		SshAuthorizedKeys: d.SSHKeys,
		AdbAuthorizedKeys: d.ADBKeys,
		Params:            d.Params,
		Revision:          d.Revision,
		Power:             agentv1.PowerState_POWER_STATE_RUNNING,
		License:           &agentv1.LicenseSpec{Mode: agentv1.LicenseMode_LICENSE_MODE_NONE},
	}
	if d.Power == model.PowerStopped {
		spec.Power = agentv1.PowerState_POWER_STATE_STOPPED
	}
	switch d.LicenseMode {
	case "user_key":
		spec.License = &agentv1.LicenseSpec{Mode: agentv1.LicenseMode_LICENSE_MODE_USER_KEY, UserKey: key}
	case "pool":
		spec.License = &agentv1.LicenseSpec{Mode: agentv1.LicenseMode_LICENSE_MODE_POOL, Pool: d.LicensePool}
	}
	for _, svc := range d.Services {
		ss := &agentv1.ServiceSpec{Name: svc.Name, InternalPort: int32(svc.InternalPort), PublicPort: int32(svc.PublicPort)}
		if model.CredentialServices[svc.Name] {
			ss.Username, ss.Password = d.Username, password
		}
		spec.Services = append(spec.Services, ss)
	}
	return spec, nil
}
