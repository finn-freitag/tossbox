// Package scheduler implements quota checks, node capacity accounting and
// node/driver selection (DESIGN.md §8, §9, §14). It is pure: callers pass in
// snapshots of nodes and devices.
package scheduler

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/finn-freitag/tossbox/internal/model"
)

// DriverCaps is the scheduler's view of one driver on a node.
type DriverCaps struct {
	Families   []string
	Services   []string
	FixedSlots int // > 0: count-based driver (orchard, usb-phone)
	Healthy    bool
	// NetworkPolicies the driver enforces; empty = internet only (old agents).
	NetworkPolicies []string
	GPUMode         string   // "" = no GPU
	GPUSlots        int      // -1 = shared, otherwise devices with a GPU at once
	GPUFamilies     []string // families that can use the GPU; empty = all
}

// DeviceLoad is a device already placed on a node.
type DeviceLoad struct {
	Driver      string
	Res         model.Resources
	Stopped     bool
	LicensePool string // pool lease, if any
	GPU         bool
}

// Limits from the node config (0 = host total / 100%).
type Limits struct {
	RAMMB      int64
	CPUPercent int
	StorageGB  int64
}

// Node is a snapshot of a node.
type Node struct {
	ID            string
	Name          string
	Online        bool
	Arch          string
	HostCores     int
	HostRAMMB     int64
	HostStorageGB int64
	Limits        Limits
	Drivers       map[string]DriverCaps
	LicensePools  map[string]int // pool -> total keys
	StorageUsedGB int64          // actual usage reported by the agent
	Devices       []DeviceLoad
	LocalImages   []string // images for local:<name> template images
}

// CPUPercent returns the share of node CPU a device accounts for: its cap if
// set, otherwise vcpus / host cores.
func CPUPercent(r model.Resources, hostCores int) float64 {
	if r.CPUCapPercent > 0 {
		return float64(r.CPUCapPercent)
	}
	if hostCores <= 0 {
		hostCores = 1
	}
	return float64(r.VCPUs) * 100 / float64(hostCores)
}

// Capacity is the effective capacity of a node (limits x overcommit ratios).
type Capacity struct {
	RAMMB      int64   // 0 = unknown/unlimited
	CPUPercent float64 // of host CPU
	StorageGB  int64   // 0 = unlimited
	VCPUs      int
}

// Capacity computes the effective capacity under the given settings.
func (n *Node) Capacity(s model.Settings) Capacity {
	ram := n.Limits.RAMMB
	if ram <= 0 {
		ram = n.HostRAMMB
	}
	cpu := float64(n.Limits.CPUPercent)
	if cpu <= 0 {
		cpu = 100
	}
	vcpus := n.HostCores
	if s.OvercommitRAM && s.RAMOvercommitRatio > 1 {
		ram = int64(float64(ram) * s.RAMOvercommitRatio)
	}
	if s.OvercommitCPU && s.CPUOvercommitRatio > 1 {
		cpu *= s.CPUOvercommitRatio
		vcpus = int(float64(vcpus) * s.CPUOvercommitRatio)
	}
	st := n.Limits.StorageGB
	if st <= 0 {
		st = n.HostStorageGB
	}
	return Capacity{RAMMB: ram, CPUPercent: cpu, StorageGB: st, VCPUs: vcpus}
}

// Allocation is what devices on a node account for.
type Allocation struct {
	VCPUs      int
	CPUPercent float64
	RAMMB      int64
	StorageGB  int64
	Devices    int
	PerDriver  map[string]int
	Pools      map[string]int
}

// Allocated sums the reservations of the node's devices. Devices of
// count-based drivers don't consume resources. Stopped devices release CPU
// and RAM when the settings say so; storage is always kept.
func (n *Node) Allocated(s model.Settings) Allocation {
	a := Allocation{PerDriver: map[string]int{}, Pools: map[string]int{}}
	for _, d := range n.Devices {
		a.Devices++
		a.PerDriver[d.Driver]++
		if d.LicensePool != "" {
			a.Pools[d.LicensePool]++
		}
		if n.Drivers[d.Driver].FixedSlots > 0 {
			continue
		}
		a.StorageGB += d.Res.StorageGB
		if d.Stopped && !s.StoppedKeepsReservation {
			continue
		}
		a.VCPUs += d.Res.VCPUs
		a.CPUPercent += CPUPercent(d.Res, n.HostCores)
		a.RAMMB += d.Res.RAMMB
	}
	return a
}

// Free is the remaining capacity of a node.
type Free struct {
	VCPUs      int     `json:"vcpus"`
	CPUPercent float64 `json:"cpu_percent"`
	RAMMB      int64   `json:"ram_mb"`
	StorageGB  int64   `json:"storage_gb"`
}

// Free computes the remaining capacity (never negative).
func (n *Node) Free(s model.Settings) Free {
	c, a := n.Capacity(s), n.Allocated(s)
	storageUsed := a.StorageGB
	if s.StorageAccounting == "actual" {
		storageUsed = n.StorageUsedGB
	}
	return Free{
		VCPUs:      max(0, c.VCPUs-a.VCPUs),
		CPUPercent: max(0, c.CPUPercent-a.CPUPercent),
		RAMMB:      max(0, c.RAMMB-a.RAMMB),
		StorageGB:  max(0, c.StorageGB-storageUsed),
	}
}

// FreeSlots returns free slots per count-based driver.
func (n *Node) FreeSlots(s model.Settings) map[string]int {
	out := map[string]int{}
	a := n.Allocated(s)
	for name, d := range n.Drivers {
		if d.FixedSlots > 0 {
			out[name] = max(0, d.FixedSlots-a.PerDriver[name])
		}
	}
	return out
}

// FreePool returns the number of free keys of a license pool on the node.
func (n *Node) FreePool(pool string, s model.Settings) int {
	return max(0, n.LicensePools[pool]-n.Allocated(s).Pools[pool])
}

// Request describes a device to place.
type Request struct {
	Family      string
	Arch        []string // template archs; empty = any
	Drivers     []string // candidate drivers (template drivers ∩ allowed), or the one requested
	NodeID      string   // pinned node, optional
	Res         model.Resources
	Services    []string
	LicensePool string
	// NetworkPolicy "" means internet.
	NetworkPolicy string
	GPU           bool
	// Images maps a driver to the template's image URL for it; local:<name>
	// images must exist on the node.
	Images map[string]string
}

// Placement is the scheduler's decision.
type Placement struct {
	NodeID string
	Driver string
}

// Pick chooses a node and driver. Among eligible nodes it prefers the one
// with the most free RAM relative to its capacity (least loaded); count-based
// drivers prefer the node with the most free slots.
func Pick(nodes []Node, req Request, s model.Settings) (Placement, error) {
	type cand struct {
		p     Placement
		score float64
	}
	var cands []cand
	var reasons []string
	for i := range nodes {
		n := &nodes[i]
		if req.NodeID != "" && n.ID != req.NodeID {
			continue
		}
		if !n.Online {
			reasons = append(reasons, n.Name+": offline")
			continue
		}
		if len(req.Arch) > 0 && n.Arch != "" && !model.Allows(req.Arch, n.Arch) {
			reasons = append(reasons, fmt.Sprintf("%s: architecture %s not supported by template", n.Name, n.Arch))
			continue
		}
		for _, drv := range req.Drivers {
			caps, ok := n.Drivers[drv]
			if !ok {
				continue
			}
			if why := fits(n, drv, caps, req, s); why != "" {
				reasons = append(reasons, n.Name+"/"+drv+": "+why)
				continue
			}
			var score float64
			if caps.FixedSlots > 0 {
				score = float64(n.FreeSlots(s)[drv]) / float64(caps.FixedSlots)
			} else if c := n.Capacity(s); c.RAMMB > 0 {
				score = float64(n.Free(s).RAMMB) / float64(c.RAMMB)
			}
			cands = append(cands, cand{Placement{n.ID, drv}, score})
		}
	}
	if len(cands) == 0 {
		msg := "no node can host this device"
		if req.NodeID != "" && len(reasons) == 0 {
			msg = "the selected node doesn't offer a compatible driver"
		}
		if len(reasons) > 0 {
			msg += ": " + strings.Join(reasons, "; ")
		}
		return Placement{}, model.Errf(model.CodeNoCapacity, "%s", msg)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	return cands[0].p, nil
}

func fits(n *Node, drv string, caps DriverCaps, req Request, s model.Settings) string {
	if !caps.Healthy {
		return "driver unhealthy"
	}
	if req.Family != "" && len(caps.Families) > 0 && !model.Allows(caps.Families, req.Family) {
		return "family " + req.Family + " not supported"
	}
	for _, svc := range req.Services {
		if !model.Allows(caps.Services, svc) || len(caps.Services) == 0 {
			return "service " + svc + " not supported"
		}
	}
	if p := req.NetworkPolicy; p != "" && p != model.NetworkInternet && !slices.Contains(caps.NetworkPolicies, p) {
		return "network policy " + req.NetworkPolicy + " not supported"
	}
	if name, ok := strings.CutPrefix(req.Images[drv], "local:"); ok && !slices.Contains(n.LocalImages, name) {
		return "image " + name + " is not on this node (build it with Packer, see tb-templates/packer)"
	}
	if req.GPU {
		if caps.GPUMode == "" {
			return "no GPU"
		}
		if req.Family != "" && len(caps.GPUFamilies) > 0 && !slices.Contains(caps.GPUFamilies, req.Family) {
			return "the GPU (" + caps.GPUMode + ") can't be used by " + req.Family + " guests"
		}
		if caps.GPUSlots > 0 {
			// SR-IOV virtual functions are one pool per node, shared by
			// all drivers that hand them out.
			used := 0
			for _, d := range n.Devices {
				if !d.GPU {
					continue
				}
				if d.Driver == drv || (caps.GPUMode == "sriov" && n.Drivers[d.Driver].GPUMode == "sriov") {
					used++
				}
			}
			if used >= caps.GPUSlots {
				return fmt.Sprintf("all %d GPU slots in use", caps.GPUSlots)
			}
		}
	}
	if req.LicensePool != "" && n.FreePool(req.LicensePool, s) < 1 {
		return "no free key in license pool " + req.LicensePool
	}
	if caps.FixedSlots > 0 {
		if n.FreeSlots(s)[drv] < 1 {
			return "no free slot"
		}
		return ""
	}
	c, free := n.Capacity(s), n.Free(s)
	if c.RAMMB > 0 && req.Res.RAMMB > free.RAMMB {
		return fmt.Sprintf("not enough RAM (%d MB free)", free.RAMMB)
	}
	if CPUPercent(req.Res, n.HostCores) > free.CPUPercent+1e-9 {
		return fmt.Sprintf("not enough CPU (%.0f%% free)", free.CPUPercent)
	}
	if n.HostCores > 0 && req.Res.VCPUs > n.HostCores {
		return fmt.Sprintf("more vCPUs than host cores (%d)", n.HostCores)
	}
	if c.StorageGB > 0 && req.Res.StorageGB > free.StorageGB {
		return fmt.Sprintf("not enough storage (%d GB free)", free.StorageGB)
	}
	return ""
}

// QuotaRequest is what a new device adds to a user's usage.
type QuotaRequest struct {
	Res      model.Resources
	Phone    bool
	GPU      bool
	TTLHours int
}

// CheckQuota verifies that a user may create one more device.
func CheckQuota(q model.Quota, u model.Usage, r QuotaRequest) error {
	over := func(what string, used, add, limit int64) error {
		if limit > 0 && used+add > limit {
			return model.Errf(model.CodeQuotaExceeded, "quota exceeded: %s (%d used + %d requested > %d)", what, used, add, limit)
		}
		return nil
	}
	checks := []error{
		over("machines", int64(u.Machines), 1, int64(q.MaxMachines)),
		over("vCPUs", int64(u.VCPUs), int64(r.Res.VCPUs), int64(q.MaxVCPUs)),
		over("RAM (MB)", u.RAMMB, r.Res.RAMMB, q.MaxRAMMB),
		over("storage (GB)", u.StorageGB, r.Res.StorageGB, q.MaxStorageGB),
	}
	if r.Phone {
		checks = append(checks, over("phones", int64(u.Phones), 1, int64(q.MaxPhones)))
	}
	if r.GPU {
		checks = append(checks, over("GPUs", int64(u.GPUs), 1, int64(q.MaxGPUs)))
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	if q.MaxTTLHours > 0 && (r.TTLHours == 0 || r.TTLHours > q.MaxTTLHours) {
		return model.Errf(model.CodeQuotaExceeded, "ttl must be between 1 and %d hours", q.MaxTTLHours)
	}
	return nil
}
