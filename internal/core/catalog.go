package core

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/finn-freitag/tossbox/internal/catalog"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/scheduler"
)

// TemplateView is the API representation of a template.
type TemplateView struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Family            string            `json:"family"`
	Version           string            `json:"version"`
	Icon              string            `json:"icon"`
	CompatibleDrivers []string          `json:"compatible_drivers"`
	AvailableDrivers  []string          `json:"available_drivers"`
	SupportedServices []string          `json:"supported_services"`
	DefaultServices   []string          `json:"default_services"`
	MinResources      catalog.Resources `json:"min_resources"`
	DefaultResources  catalog.Resources `json:"default_resources"`
	License           bool              `json:"license"`
	Source            string            `json:"source"`
	// GPU is the template's GPU need: "required", "recommended" or "".
	GPU string `json:"gpu"`
}

// availableDrivers returns the template's drivers that some online node can
// run: healthy driver and, for local:<name> images, the image on the node.
func availableDrivers(t *catalog.Template, nodes []scheduler.Node) map[string]bool {
	out := map[string]bool{}
	for _, n := range nodes {
		if !n.Online {
			continue
		}
		for name, d := range n.Drivers {
			img, ok := t.Images[name]
			if !ok || !d.Healthy {
				continue
			}
			if local, isLocal := strings.CutPrefix(img.URL, "local:"); isLocal && !slices.Contains(n.LocalImages, local) {
				continue
			}
			out[name] = true
		}
	}
	return out
}

// Templates returns the templates visible to the principal.
func (s *Service) Templates(ctx context.Context, p Principal) ([]*TemplateView, error) {
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	snaps, _, err := s.nodeSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	return s.templateViews(p, st, snaps), nil
}

func (s *Service) templateViews(p Principal, st model.Settings, nodes []scheduler.Node) []*TemplateView {
	q := p.User.Quota
	out := []*TemplateView{}
	for _, t := range s.catalog.All() {
		if !model.Allows(q.AllowedTemplates, t.ID) {
			continue
		}
		var allowed, avail []string
		online := availableDrivers(t, nodes)
		for _, d := range t.Drivers() {
			if model.Allows(q.AllowedDrivers, d) {
				allowed = append(allowed, d)
				if online[d] {
					avail = append(avail, d)
				}
			}
		}
		if len(allowed) == 0 {
			continue
		}
		out = append(out, &TemplateView{
			ID: t.ID, Name: t.Name, Description: t.Description, Family: t.Family, Version: t.Version, Icon: t.Icon,
			CompatibleDrivers: t.Drivers(), AvailableDrivers: nonNil(avail),
			SupportedServices: nonNil(t.SupportedServices), DefaultServices: DefaultServicesFor(t, st),
			MinResources: t.MinResources, DefaultResources: t.DefaultResources, License: t.License, Source: t.Source,
			GPU: t.Params["gpu"],
		})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ReloadTemplates re-reads all template sources.
func (s *Service) ReloadTemplates(ctx context.Context) (int, []string) {
	n, errs := s.catalog.Reload(ctx)
	if errs == nil {
		errs = []string{}
	}
	for _, e := range errs {
		s.log.Warn("template source", "err", e)
	}
	return n, errs
}

// CreateNodeOption is a node as shown in the create form.
type CreateNodeOption struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Status         string              `json:"status"`
	Drivers        []string            `json:"drivers"`
	Free           scheduler.Free      `json:"free"`
	FixedSlotsFree map[string]int      `json:"fixed_slots_free"`
	LicensePools   []LicensePoolOption `json:"license_pools"`
	HostCores      int                 `json:"host_cores"`
}

// LicensePoolOption is a license pool with its free key count.
type LicensePoolOption struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Free int    `json:"free"`
}

// ServiceOption is an entry of the service catalog.
type ServiceOption struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Default     bool   `json:"default"`
	DesktopOnly bool   `json:"desktop_only"`
	Warning     string `json:"warning,omitempty"`
}

// CreateOptions is the response of GET /api/create-options.
type CreateOptions struct {
	Templates        []*TemplateView     `json:"templates"`
	Nodes            []CreateNodeOption  `json:"nodes"`
	LicensePools     []LicensePoolOption `json:"license_pools"`
	Quota            model.Quota         `json:"quota"`
	Usage            model.Usage         `json:"usage"`
	AllowedExposures []string            `json:"allowed_exposures"`
	DriverServices   map[string][]string `json:"driver_services"`
	DefaultTTLHours  int                 `json:"default_ttl_hours"`
	MaxTTLHours      int                 `json:"max_ttl_hours"`
	ServiceCatalog   []ServiceOption     `json:"service_catalog"`
	// DriverFeatures: per driver, what online healthy nodes offer.
	DriverFeatures map[string]DriverFeature `json:"driver_features"`
}

// DriverFeature summarizes optional driver features across online nodes.
type DriverFeature struct {
	NetworkPolicies []string `json:"network_policies"`
	GPU             bool     `json:"gpu"`
	// GPUFamilies that can use the GPU (empty = all).
	GPUFamilies []string `json:"gpu_families"`
}

// CreateOptionsFor returns everything the create form needs.
func (s *Service) CreateOptionsFor(ctx context.Context, p Principal) (*CreateOptions, error) {
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	snaps, nodes, err := s.nodeSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.Usage(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	q := p.User.Quota
	poolAllowed := func(name string) bool { return p.IsAdmin() || slices.Contains(q.AllowedLicensePools, name) }

	out := &CreateOptions{
		Templates: s.templateViews(p, st, snaps),
		Nodes:     []CreateNodeOption{}, LicensePools: []LicensePoolOption{},
		Quota: q, Usage: usage, DriverServices: map[string][]string{}, DriverFeatures: map[string]DriverFeature{},
		DefaultTTLHours: st.DefaultTTLHours, MaxTTLHours: q.MaxTTLHours,
	}
	for _, e := range model.Exposures {
		if model.Allows(q.AllowedExposures, e) && (e != model.ExposureTailscale || s.cfg.GatewayTailscaleAddress != "") {
			out.AllowedExposures = append(out.AllowedExposures, e)
		}
	}
	poolFree := map[string]*LicensePoolOption{}
	driverSvc := map[string]map[string]bool{}
	for i, sn := range snaps {
		caps := parseCaps(nodes[i].Capabilities)
		opt := CreateNodeOption{
			ID: sn.ID, Name: sn.Name, Status: s.nodeStatus(nodes[i]), Drivers: []string{},
			Free: sn.Free(st), FixedSlotsFree: sn.FreeSlots(st), LicensePools: []LicensePoolOption{}, HostCores: sn.HostCores,
		}
		for name, d := range sn.Drivers {
			if !model.Allows(q.AllowedDrivers, name) {
				continue
			}
			opt.Drivers = append(opt.Drivers, name)
			if sn.Online && d.Healthy {
				f := out.DriverFeatures[name]
				pols := d.NetworkPolicies
				if len(pols) == 0 {
					pols = []string{model.NetworkInternet}
				}
				for _, p := range pols {
					if !slices.Contains(f.NetworkPolicies, p) {
						f.NetworkPolicies = append(f.NetworkPolicies, p)
					}
				}
				if d.GPUMode != "" {
					if !f.GPU || len(f.GPUFamilies) > 0 {
						if len(d.GPUFamilies) == 0 {
							f.GPUFamilies = nil // some node serves every family
						} else {
							for _, fam := range d.GPUFamilies {
								if !slices.Contains(f.GPUFamilies, fam) {
									f.GPUFamilies = append(f.GPUFamilies, fam)
								}
							}
						}
					}
					f.GPU = true
				}
				out.DriverFeatures[name] = f
				if driverSvc[name] == nil {
					driverSvc[name] = map[string]bool{}
				}
				for _, svc := range d.Services {
					driverSvc[name][svc] = true
				}
			}
		}
		sort.Strings(opt.Drivers)
		for _, lp := range caps.LicensePools {
			if !poolAllowed(lp.Name) {
				continue
			}
			free := sn.FreePool(lp.Name, st)
			opt.LicensePools = append(opt.LicensePools, LicensePoolOption{Name: lp.Name, Type: lp.Type, Free: free})
			if sn.Online {
				if poolFree[lp.Name] == nil {
					poolFree[lp.Name] = &LicensePoolOption{Name: lp.Name, Type: lp.Type}
				}
				poolFree[lp.Name].Free += free
			}
		}
		out.Nodes = append(out.Nodes, opt)
	}
	for name, set := range driverSvc {
		for _, svc := range model.Services {
			if set[svc] {
				out.DriverServices[name] = append(out.DriverServices[name], svc)
			}
		}
	}
	for _, lp := range poolFree {
		out.LicensePools = append(out.LicensePools, *lp)
	}
	sort.Slice(out.LicensePools, func(i, j int) bool { return out.LicensePools[i].Name < out.LicensePools[j].Name })
	if out.AllowedExposures == nil {
		out.AllowedExposures = []string{}
	}
	for _, svc := range model.Services {
		o := ServiceOption{Name: svc, Default: slices.Contains(st.DefaultServices, svc) || svc == "adb"}
		switch svc {
		case "ssh":
			o.Label = "SSH"
		case "rdp":
			o.Label, o.DesktopOnly = "RDP", true
		case "vnc":
			o.Label, o.DesktopOnly = "VNC", true
			o.Warning = "VNC is unencrypted; prefer Guacamole, an SSH tunnel or Tailscale"
		case "adb":
			o.Label = "ADB"
			o.Warning = "Only your adb keys (Profile → ADB keys) can connect"
		case "http":
			o.Label = "HTTP (port 80)"
		case "https":
			o.Label = "HTTPS (port 443)"
		}
		out.ServiceCatalog = append(out.ServiceCatalog, o)
	}
	return out, nil
}
