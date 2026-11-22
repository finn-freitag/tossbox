package scheduler

import (
	"strings"
	"testing"

	"github.com/finn-freitag/tossbox/internal/model"
)

func homelab() Node {
	return Node{
		ID: "n1", Name: "homelab", Online: true, Arch: "x86_64", HostCores: 10,
		HostRAMMB: 65536, HostStorageGB: 1000,
		Limits: Limits{RAMMB: 16384, CPUPercent: 70, StorageGB: 300},
		Drivers: map[string]DriverCaps{
			"libvirt": {Families: []string{"linux", "windows"}, Services: []string{"ssh", "rdp", "vnc", "http", "https"}, Healthy: true},
		},
		LicensePools: map[string]int{"win": 1},
	}
}

func req(ram int64, cpu int, st int64) Request {
	return Request{Family: "windows", Drivers: []string{"libvirt"}, Res: model.Resources{VCPUs: 4, CPUCapPercent: cpu, RAMMB: ram, StorageGB: st}, Services: []string{"ssh", "rdp"}}
}

func TestPickExampleFromDesign(t *testing.T) {
	s := model.DefaultSettings()
	s.OvercommitCPU = false
	n := homelab()
	// windowsGitlabBuildMachine1: 8 GB RAM, 50% CPU, 150 GB storage fits.
	p, err := Pick([]Node{n}, req(8192, 50, 150), s)
	if err != nil || p.NodeID != "n1" || p.Driver != "libvirt" {
		t.Fatalf("got %v %v", p, err)
	}
	n.Devices = append(n.Devices, DeviceLoad{Driver: "libvirt", Res: model.Resources{VCPUs: 4, CPUCapPercent: 50, RAMMB: 8192, StorageGB: 150}})
	// Second one: RAM would fit (16G cap), CPU 50+50 > 70 doesn't.
	if _, err := Pick([]Node{n}, req(8192, 50, 150), s); err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("want CPU error, got %v", err)
	}
	// With 20% CPU: storage 150+160 > 300 fails.
	if _, err := Pick([]Node{n}, req(4096, 20, 160), s); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("want storage error, got %v", err)
	}
	// RAM: 8192 + 9000 > 16384.
	if _, err := Pick([]Node{n}, req(9000, 10, 10), s); err == nil || !strings.Contains(err.Error(), "RAM") {
		t.Fatalf("want RAM error, got %v", err)
	}
}

func TestOvercommitAndStopped(t *testing.T) {
	s := model.DefaultSettings()
	n := homelab()
	n.Devices = []DeviceLoad{{Driver: "libvirt", Res: model.Resources{VCPUs: 2, RAMMB: 16384, StorageGB: 10}, Stopped: true}}
	if _, err := Pick([]Node{n}, req(1024, 10, 10), s); err == nil {
		t.Fatal("stopped device keeps reservation by default")
	}
	s.StoppedKeepsReservation = false
	if _, err := Pick([]Node{n}, req(1024, 10, 10), s); err != nil {
		t.Fatalf("stopped device should release RAM: %v", err)
	}
	s.StoppedKeepsReservation = true
	s.OvercommitRAM, s.RAMOvercommitRatio = true, 1.5
	if _, err := Pick([]Node{n}, req(4096, 10, 10), s); err != nil {
		t.Fatalf("RAM overcommit should allow it: %v", err)
	}
	// CPU overcommit (default on, ratio 4): 70% * 4 = 280%.
	s = model.DefaultSettings()
	n.Devices = nil
	n.Limits.RAMMB = 0
	for i := 0; i < 5; i++ {
		n.Devices = append(n.Devices, DeviceLoad{Driver: "libvirt", Res: model.Resources{VCPUs: 1, CPUCapPercent: 50, RAMMB: 100}})
	}
	if _, err := Pick([]Node{n}, req(100, 30, 1), s); err != nil {
		t.Fatalf("cpu overcommit: %v", err)
	}
}

func TestServicesFamilyPoolsSlots(t *testing.T) {
	s := model.DefaultSettings()
	n := homelab()
	r := req(1024, 10, 10)
	r.Services = []string{"adb"}
	if _, err := Pick([]Node{n}, r, s); err == nil {
		t.Fatal("adb unsupported")
	}
	r = req(1024, 10, 10)
	r.Family = "macos"
	if _, err := Pick([]Node{n}, r, s); err == nil {
		t.Fatal("macos unsupported")
	}
	r = req(1024, 10, 10)
	r.LicensePool = "win"
	if _, err := Pick([]Node{n}, r, s); err != nil {
		t.Fatal(err)
	}
	n.Devices = []DeviceLoad{{Driver: "libvirt", LicensePool: "win", Res: model.Resources{VCPUs: 1, RAMMB: 1}}}
	if _, err := Pick([]Node{n}, r, s); err == nil {
		t.Fatal("pool exhausted")
	}

	mac := Node{ID: "m", Name: "mac", Online: true, Arch: "arm64", HostCores: 8, HostRAMMB: 16384,
		Drivers: map[string]DriverCaps{"orchard": {Families: []string{"macos"}, Services: []string{"ssh", "vnc"}, FixedSlots: 2, Healthy: true}}}
	mr := Request{Family: "macos", Drivers: []string{"orchard"}, Res: model.Resources{VCPUs: 4, RAMMB: 999999}, Services: []string{"ssh"}}
	for i := 0; i < 2; i++ {
		if _, err := Pick([]Node{mac}, mr, s); err != nil {
			t.Fatalf("slot %d: %v", i, err)
		}
		mac.Devices = append(mac.Devices, DeviceLoad{Driver: "orchard"})
	}
	if _, err := Pick([]Node{mac}, mr, s); err == nil {
		t.Fatal("only 2 macOS VMs per Mac")
	}
}

func TestLeastLoaded(t *testing.T) {
	s := model.DefaultSettings()
	a, b := homelab(), homelab()
	b.ID, b.Name = "n2", "pc"
	a.Devices = []DeviceLoad{{Driver: "libvirt", Res: model.Resources{VCPUs: 1, RAMMB: 8000}}}
	p, err := Pick([]Node{a, b}, req(1024, 10, 10), s)
	if err != nil || p.NodeID != "n2" {
		t.Fatalf("want n2, got %v %v", p, err)
	}
	a.Online = false
	if p, _ := Pick([]Node{a}, req(1024, 10, 10), s); p.NodeID != "" {
		t.Fatal("offline node picked")
	}
}

func TestQuota(t *testing.T) {
	q := model.Quota{MaxMachines: 2, MaxRAMMB: 8192, MaxPhones: 1, MaxTTLHours: 48}
	u := model.Usage{Machines: 1, RAMMB: 4096}
	if err := CheckQuota(q, u, QuotaRequest{Res: model.Resources{RAMMB: 4096}, TTLHours: 24}); err != nil {
		t.Fatal(err)
	}
	if err := CheckQuota(q, u, QuotaRequest{Res: model.Resources{RAMMB: 4097}, TTLHours: 24}); err == nil {
		t.Fatal("ram quota")
	}
	if err := CheckQuota(q, model.Usage{Machines: 2}, QuotaRequest{TTLHours: 1}); err == nil {
		t.Fatal("machine quota")
	}
	if err := CheckQuota(q, u, QuotaRequest{TTLHours: 0}); err == nil {
		t.Fatal("ttl required")
	}
	if err := CheckQuota(q, model.Usage{Phones: 1}, QuotaRequest{Phone: true, TTLHours: 1}); err == nil {
		t.Fatal("phone quota")
	}
	if err := CheckQuota(model.Quota{}, model.Usage{Machines: 1000}, QuotaRequest{}); err != nil {
		t.Fatal("zero = unlimited")
	}
	if e := model.AsError(CheckQuota(q, model.Usage{Machines: 2}, QuotaRequest{TTLHours: 1})); e.Status() != 422 {
		t.Fatal("status")
	}
}
