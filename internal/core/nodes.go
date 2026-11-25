package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/finn-freitag/tb-proto/enroll"
	agentv1 "github.com/finn-freitag/tb-proto/gen/agent/v1"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/scheduler"
	"github.com/finn-freitag/tossbox/internal/secrets"
	"github.com/finn-freitag/tossbox/internal/store"
	"github.com/google/uuid"
)

// EnrollmentTokenTTL is how long a node enrollment token stays valid.
const EnrollmentTokenTTL = 7 * 24 * time.Hour

// NodeAllocated is the allocation part of a node view.
type NodeAllocated struct {
	VCPUs      int     `json:"vcpus"`
	CPUPercent float64 `json:"cpu_percent"`
	RAMMB      int64   `json:"ram_mb"`
	StorageGB  int64   `json:"storage_gb"`
	Devices    int     `json:"devices"`
}

// NodeView is the admin API representation of a node.
type NodeView struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Address      string                 `json:"address"`
	Status       string                 `json:"status"`
	AgentVersion string                 `json:"agent_version"`
	LastSeen     *time.Time             `json:"last_seen"`
	Enrolled     bool                   `json:"enrolled"`
	Capabilities model.NodeCapabilities `json:"capabilities"`
	Allocated    NodeAllocated          `json:"allocated"`
	Usage        model.NodeUsage        `json:"usage"`
}

// NodeTokenResponse is returned when creating a node or regenerating its token.
type NodeTokenResponse struct {
	Node            *NodeView `json:"node"`
	EnrollmentToken string    `json:"enrollment_token"`
	GRPCEndpoint    string    `json:"grpc_endpoint"`
}

func (s *Service) nodeStatus(n *store.Node) string {
	switch {
	case s.link.Online(n.ID):
		return "online"
	case !n.Enrolled:
		return "pending"
	default:
		return "offline"
	}
}

func (s *Service) nodeView(ctx context.Context, n *store.Node, st model.Settings) *NodeView {
	devs, _ := s.store.ListNodeDevices(ctx, n.ID)
	return s.nodeViewWith(n, devs, st)
}

func (s *Service) nodeViewWith(n *store.Node, devs []*store.Device, st model.Settings) *NodeView {
	var loads []scheduler.DeviceLoad
	for _, d := range devs {
		loads = append(loads, scheduler.DeviceLoad{Driver: d.Driver, Res: d.Res, Stopped: d.Power == model.PowerStopped, LicensePool: d.LicensePool})
	}
	snap := snapshot(n, loads, s.link.Online(n.ID))
	a := snap.Allocated(st)
	return &NodeView{
		ID: n.ID, Name: n.Name, Address: n.Address, Status: s.nodeStatus(n),
		AgentVersion: n.AgentVersion, LastSeen: n.LastSeen, Enrolled: n.Enrolled,
		Capabilities: parseCaps(n.Capabilities),
		Allocated:    NodeAllocated{VCPUs: a.VCPUs, CPUPercent: a.CPUPercent, RAMMB: a.RAMMB, StorageGB: a.StorageGB, Devices: a.Devices},
		Usage:        parseUsage(n.Usage),
	}
}

// ListNodes returns all nodes (admin).
func (s *Service) ListNodes(ctx context.Context) ([]*NodeView, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := []*NodeView{}
	for _, n := range nodes {
		out = append(out, s.nodeView(ctx, n, st))
	}
	return out, nil
}

// GetNode returns one node (admin).
func (s *Service) GetNode(ctx context.Context, id string) (*NodeView, error) {
	n, err := s.getNode(ctx, id)
	if err != nil {
		return nil, err
	}
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return s.nodeView(ctx, n, st), nil
}

func (s *Service) getNode(ctx context.Context, id string) (*store.Node, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, model.Errf(model.CodeNotFound, "node not found")
	}
	n, err := s.store.GetNode(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, model.Errf(model.CodeNotFound, "node not found")
	}
	return n, err
}

func validateNode(name, address string) error {
	if strings.TrimSpace(name) == "" || len(name) > 64 {
		return model.Errf(model.CodeValidation, "name is required (max 64 characters)")
	}
	if strings.TrimSpace(address) == "" || strings.ContainsAny(address, " /") {
		return model.Errf(model.CodeValidation, "address must be a host name or IP the gateway can reach")
	}
	return nil
}

func (s *Service) newToken() (enroll.Token, string, error) {
	secret, err := secrets.Token("")
	if err != nil {
		return enroll.Token{}, "", err
	}
	return enroll.Token{CAFingerprint: s.ca.Fingerprint(), Secret: secret}, secrets.Hash(secret), nil
}

// CreateNode registers a node and returns its enrollment token (admin).
func (s *Service) CreateNode(ctx context.Context, p Principal, name, address string) (*NodeTokenResponse, error) {
	name, address = strings.TrimSpace(name), strings.TrimSpace(address)
	if err := validateNode(name, address); err != nil {
		return nil, err
	}
	tok, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	n := &store.Node{ID: uuid.NewString(), Name: name, Address: address}
	if err := s.store.CreateNode(ctx, n, hash, time.Now().Add(EnrollmentTokenTTL)); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, model.Errf(model.CodeConflict, "a node named %q already exists", name)
		}
		return nil, err
	}
	s.store.Audit(ctx, p.User.ID, "node.create", n.ID, map[string]any{"name": name})
	st, _ := s.store.GetSettings(ctx)
	return &NodeTokenResponse{Node: s.nodeView(ctx, n, st), EnrollmentToken: tok.String(), GRPCEndpoint: s.cfg.GRPCEndpoint}, nil
}

// UpdateNode changes name/address (admin).
func (s *Service) UpdateNode(ctx context.Context, id string, name, address *string) (*NodeView, error) {
	n, err := s.getNode(ctx, id)
	if err != nil {
		return nil, err
	}
	nn, na := n.Name, n.Address
	if name != nil {
		nn = strings.TrimSpace(*name)
	}
	if address != nil {
		na = strings.TrimSpace(*address)
	}
	if err := validateNode(nn, na); err != nil {
		return nil, err
	}
	if err := s.store.UpdateNodeMeta(ctx, id, nn, na); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, model.Errf(model.CodeConflict, "a node named %q already exists", nn)
		}
		return nil, err
	}
	s.SyncGateway(ctx)
	return s.GetNode(ctx, id)
}

// RegenerateNodeToken revokes the node certificate and issues a new token (admin).
func (s *Service) RegenerateNodeToken(ctx context.Context, p Principal, id string) (*NodeTokenResponse, error) {
	if _, err := s.getNode(ctx, id); err != nil {
		return nil, err
	}
	tok, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	if err := s.store.ResetNodeToken(ctx, id, hash, time.Now().Add(EnrollmentTokenTTL)); err != nil {
		return nil, err
	}
	s.link.Disconnect(id)
	s.store.Audit(ctx, p.User.ID, "node.token", id, nil)
	nv, err := s.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}
	return &NodeTokenResponse{Node: nv, EnrollmentToken: tok.String(), GRPCEndpoint: s.cfg.GRPCEndpoint}, nil
}

// DeleteNode removes a node without devices (admin).
func (s *Service) DeleteNode(ctx context.Context, p Principal, id string) error {
	if _, err := s.getNode(ctx, id); err != nil {
		return err
	}
	if err := s.store.DeleteNode(ctx, id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return model.Errf(model.CodeConflict, "node still has devices")
		}
		return err
	}
	s.link.Disconnect(id)
	s.store.Audit(ctx, p.User.ID, "node.delete", id, nil)
	return nil
}

// ---------------------------------------------------------------- gRPC hooks

// EnrollResult is returned to the gRPC layer.
type EnrollResult struct {
	NodeID, NodeName string
	CertDER, CADER   []byte
}

// Enroll consumes an enrollment token secret and signs the agent's CSR.
func (s *Service) Enroll(ctx context.Context, secret string, csrDER []byte, agentVersion string) (*EnrollResult, error) {
	n, err := s.store.NodeByToken(ctx, secrets.Hash(secret))
	if err != nil {
		return nil, model.Errf(model.CodeUnauthorized, "invalid or expired enrollment token")
	}
	der, serial, err := s.ca.SignNodeCSR(csrDER, n.ID)
	if err != nil {
		return nil, model.Errf(model.CodeValidation, "%v", err)
	}
	if err := s.store.CompleteEnrollment(ctx, n.ID, serial); err != nil {
		return nil, err
	}
	s.store.Audit(ctx, "", "node.enroll", n.ID, map[string]any{"agent_version": agentVersion})
	s.log.Info("node enrolled", "node", n.Name, "id", n.ID)
	return &EnrollResult{NodeID: n.ID, NodeName: n.Name, CertDER: der, CADER: s.ca.Cert.Raw}, nil
}

// AuthenticateNode checks that a client certificate (CN, serial) belongs to
// an enrolled node and is its current certificate.
func (s *Service) AuthenticateNode(ctx context.Context, nodeID, serial string) (*store.Node, error) {
	n, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !n.Enrolled || n.CertSerial == "" || n.CertSerial != serial {
		return nil, errors.New("certificate revoked")
	}
	return n, nil
}

// CapsFromProto converts agent capabilities into the stored JSON model.
func CapsFromProto(c *agentv1.Capabilities) model.NodeCapabilities {
	out := model.NodeCapabilities{Drivers: []model.DriverInfo{}, LicensePools: []model.LicensePoolInfo{}, LocalImages: []string{}}
	if c == nil {
		return out
	}
	out.LocalImages = append(out.LocalImages, c.GetLocalImages()...)
	out.Arch, out.OS = c.GetArch(), c.GetOs()
	out.HostCores, out.HostRAMMB, out.HostStorageGB = int(c.GetHostCores()), c.GetHostRamMb(), c.GetHostStorageGb()
	if l := c.GetLimits(); l != nil {
		out.Limits = model.NodeLimits{RAMMB: l.GetRamMb(), CPUPercent: int(l.GetCpuPercent()), StorageGB: l.GetStorageGb()}
	}
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	for _, d := range c.GetDrivers() {
		di := model.DriverInfo{
			Name: d.GetName(), Families: nz(d.GetFamilies()), Services: nz(d.GetServices()), Backends: nz(d.GetBackends()),
			FixedSlots: int(d.GetFixedSlots()), Healthy: d.GetHealthy(), Message: d.GetMessage(), PhysicalDevices: []model.PhysicalDevice{},
			NetworkPolicies: nz(d.GetNetworkPolicies()), GPUMode: d.GetGpuMode(), GPUSlots: int(d.GetGpuSlots()),
			GPUFamilies: nz(d.GetGpuFamilies()),
		}
		for _, pd := range d.GetPhysicalDevices() {
			di.PhysicalDevices = append(di.PhysicalDevices, model.PhysicalDevice{Serial: pd.GetSerial(), Family: pd.GetFamily(), Model: pd.GetModel(), Available: pd.GetAvailable()})
		}
		out.Drivers = append(out.Drivers, di)
	}
	for _, p := range c.GetLicensePools() {
		out.LicensePools = append(out.LicensePools, model.LicensePoolInfo{Name: p.GetName(), Type: p.GetType(), Total: int(p.GetTotal()), InUse: int(p.GetInUse())})
	}
	return out
}

// StateName converts a proto device state into the API string.
func StateName(st agentv1.DeviceState) string {
	if st == agentv1.DeviceState_DEVICE_STATE_UNSPECIFIED {
		return "pending"
	}
	return strings.ToLower(strings.TrimPrefix(st.String(), "DEVICE_STATE_"))
}

// NodeConnected handles an agent Hello: it stores capabilities, reconciles
// reported devices with the database and returns the Welcome (full desired
// state). Devices the agent reports but that aren't in the Welcome are
// destroyed by the agent.
func (s *Service) NodeConnected(ctx context.Context, nodeID string, hello *agentv1.Hello) (*agentv1.Welcome, error) {
	n, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	caps, _ := json.Marshal(CapsFromProto(hello.GetCapabilities()))
	if err := s.store.UpdateNodeConnection(ctx, nodeID, hello.GetAgentVersion(), caps); err != nil {
		return nil, err
	}
	reported := map[string]*agentv1.DeviceStatus{}
	for _, st := range hello.GetDevices() {
		reported[st.GetDeviceId()] = st
	}
	devs, err := s.store.ListNodeDevices(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	w := &agentv1.Welcome{NodeId: nodeID, NodeName: n.Name}
	for _, d := range devs {
		st, known := reported[d.ID]
		if d.Deleting {
			if !known || st.GetState() == agentv1.DeviceState_DEVICE_STATE_DESTROYED {
				s.removeDevice(ctx, d)
			}
			continue // not in Welcome -> the agent destroys it
		}
		if known {
			s.applyStatus(ctx, d, st)
		}
		spec, err := s.spec(d)
		if err != nil {
			s.log.Error("build spec", "device", d.ID, "err", err)
			continue
		}
		w.Devices = append(w.Devices, spec)
	}
	s.log.Info("node connected", "node", n.Name, "agent_version", hello.GetAgentVersion(), "devices", len(w.Devices))
	s.publishNode(ctx, nodeID)
	s.publishNodeDevices(ctx, nodeID)
	return w, nil
}

// NodeDisconnected marks the node offline in the UI.
func (s *Service) NodeDisconnected(nodeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.log.Info("node disconnected", "node", nodeID)
	s.publishNode(ctx, nodeID)
	s.publishNodeDevices(ctx, nodeID)
}

// NodeHeartbeat stores resource usage.
func (s *Service) NodeHeartbeat(ctx context.Context, nodeID string, hb *agentv1.Heartbeat) {
	var raw json.RawMessage
	if u := hb.GetUsage(); u != nil {
		raw, _ = json.Marshal(model.NodeUsage{
			RAMUsedMB: u.GetRamUsedMb(), CPUUsedPercent: u.GetCpuUsedPercent(), StorageUsedGB: u.GetStorageUsedGb(),
			HostRAMUsedMB: u.GetHostRamUsedMb(), HostStorageUsedGB: u.GetHostStorageUsedGb(), HostStorageTotalGB: u.GetHostStorageTotalGb(),
		})
	}
	if err := s.store.TouchNode(ctx, nodeID, raw); err != nil {
		s.log.Warn("heartbeat", "node", nodeID, "err", err)
	}
	s.publishNode(ctx, nodeID)
}

// NodeCapabilitiesChanged stores new capabilities.
func (s *Service) NodeCapabilitiesChanged(ctx context.Context, nodeID string, c *agentv1.Capabilities) {
	caps, _ := json.Marshal(CapsFromProto(c))
	if err := s.store.UpdateNodeCapabilities(ctx, nodeID, caps); err != nil {
		s.log.Warn("capabilities", "node", nodeID, "err", err)
	}
	s.publishNode(ctx, nodeID)
}

// NodeDeviceStatus handles a DeviceStatus from an agent.
func (s *Service) NodeDeviceStatus(ctx context.Context, nodeID string, st *agentv1.DeviceStatus) {
	d, err := s.store.GetDevice(ctx, st.GetDeviceId())
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.NodeID != nodeID) {
		// Unknown to us: make sure the agent cleans it up.
		if st.GetState() != agentv1.DeviceState_DEVICE_STATE_DESTROYED {
			s.link.Send(nodeID, &agentv1.DashboardMessage{Msg: &agentv1.DashboardMessage_DeleteDevice{DeleteDevice: &agentv1.DeleteDevice{DeviceId: st.GetDeviceId()}}})
		}
		return
	}
	if err != nil {
		s.log.Warn("device status", "err", err)
		return
	}
	s.applyStatus(ctx, d, st)
}

func (s *Service) applyStatus(ctx context.Context, d *store.Device, st *agentv1.DeviceStatus) {
	state := StateName(st.GetState())
	if st.GetState() == agentv1.DeviceState_DEVICE_STATE_DESTROYED && d.Deleting {
		if err := s.removeDevice(ctx, d); err != nil {
			s.log.Warn("remove device", "device", d.ID, "err", err)
		}
		return
	}
	if d.Deleting {
		state = "destroying"
	}
	ok, err := s.store.UpdateDeviceStatus(ctx, d.ID, store.DeviceStatus{
		State: state, Ready: st.GetReady(), Message: st.GetMessage(), InternalIP: st.GetInternalIp(), StorageUsedMB: st.GetStorageUsedMb(),
		MemoryUsedMB: st.GetMemoryUsedMb(), CPUUsedPercent: st.GetCpuUsedPercent(),
	})
	if err != nil || !ok {
		return
	}
	if d2, err := s.store.GetDevice(ctx, d.ID); err == nil {
		s.publishDevice(ctx, d2)
	}
}

func (s *Service) publishNode(ctx context.Context, nodeID string) {
	n, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return
	}
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return
	}
	s.events.Publish("node", s.nodeView(ctx, n, st), "", true)
}

func (s *Service) publishNodeDevices(ctx context.Context, nodeID string) {
	devs, err := s.store.ListNodeDevices(ctx, nodeID)
	if err != nil {
		return
	}
	nodes := s.nodeIndex(ctx)
	for _, d := range devs {
		s.events.Publish("device", s.viewWith(d, nodes), d.OwnerID, false)
	}
}
