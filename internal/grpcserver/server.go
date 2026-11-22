// Package grpcserver implements the agent-facing gRPC API: Enrollment.Enroll
// (token + CSR, no client cert) and NodeControl.Connect (mTLS, one
// long-lived bidirectional stream per node). It also implements core.NodeLink.
package grpcserver

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	agentv1 "github.com/finn-freitag/tb-proto/gen/agent/v1"
	"github.com/finn-freitag/tossbox/internal/core"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/pki"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Timing of the control stream (see IMPLEMENTATION_NOTES.md).
var (
	PingInterval   = 30 * time.Second
	OfflineTimeout = 60 * time.Second
	HelloTimeout   = 30 * time.Second
)

// Server is the gRPC server.
type Server struct {
	agentv1.UnimplementedEnrollmentServer
	agentv1.UnimplementedNodeControlServer

	core *core.Service
	ca   *pki.CA
	log  *slog.Logger
	grpc *grpc.Server

	mu       sync.Mutex
	sessions map[string]*session
}

// session is one connected node stream.
type session struct {
	nodeID  string
	cancel  context.CancelFunc
	out     chan *agentv1.DashboardMessage
	lastMsg atomic.Int64

	// Until the Welcome has been queued, messages from the core are buffered
	// so the agent always receives Welcome first.
	mu       sync.Mutex
	welcomed bool
	pending  []*agentv1.DashboardMessage
}

// New creates the gRPC server with TLS from the dashboard CA.
func New(c *core.Service, ca *pki.CA, hosts []string, log *slog.Logger) (*Server, error) {
	cert, err := ca.ServerCert(hosts)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		// Enroll is called without a client certificate; Connect requires one
		// (checked in the handler). Given certificates are verified against the CA.
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  ca.Pool(),
		MinVersion: tls.VersionTLS12,
	}
	s := &Server{core: c, ca: ca, log: log.With("component", "grpc"), sessions: map[string]*session{}}
	s.grpc = grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 20 * time.Second}),
	)
	agentv1.RegisterEnrollmentServer(s.grpc, s)
	agentv1.RegisterNodeControlServer(s.grpc, s)
	return s, nil
}

// Serve serves on the listener until Stop.
func (s *Server) Serve(ln net.Listener) error { return s.grpc.Serve(ln) }

// Stop stops the server.
func (s *Server) Stop() { s.grpc.Stop() }

// Enroll implements Enrollment.Enroll.
func (s *Server) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	res, err := s.core.Enroll(ctx, req.GetToken(), req.GetCsrDer(), req.GetAgentVersion())
	if err != nil {
		e := model.AsError(err)
		switch e.Code {
		case model.CodeUnauthorized:
			return nil, status.Error(codes.Unauthenticated, e.Msg)
		case model.CodeValidation:
			return nil, status.Error(codes.InvalidArgument, e.Msg)
		}
		s.log.Error("enroll", "err", err)
		return nil, status.Error(codes.Internal, "enrollment failed")
	}
	return &agentv1.EnrollResponse{NodeId: res.NodeID, NodeName: res.NodeName, ClientCertDer: res.CertDER, CaCertDer: res.CADER}, nil
}

// peerNode extracts node ID (CN) and serial from the verified client cert.
func peerNode(ctx context.Context) (string, string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", "", errors.New("no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return "", "", errors.New("client certificate required")
	}
	leaf := ti.State.VerifiedChains[0][0]
	return leaf.Subject.CommonName, leaf.SerialNumber.Text(16), nil
}

// Connect implements NodeControl.Connect.
func (s *Server) Connect(stream agentv1.NodeControl_ConnectServer) error {
	nodeID, serial, err := peerNode(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	node, err := s.core.AuthenticateNode(stream.Context(), nodeID, serial)
	if err != nil {
		return status.Error(codes.PermissionDenied, "node certificate is not valid (any more); re-enroll the node")
	}
	log := s.log.With("node", node.Name)

	// Receive in a goroutine so the handler can also return on watchdog or
	// replacement (returning ends the stream and unblocks Recv).
	recv := make(chan *agentv1.AgentMessage)
	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case recv <- m:
			case <-stream.Context().Done():
				return
			}
		}
	}()

	var hello *agentv1.Hello
	select {
	case m := <-recv:
		hello = m.GetHello()
		if hello == nil {
			return status.Error(codes.FailedPrecondition, "first message must be Hello")
		}
	case err := <-recvErr:
		return err
	case <-time.After(HelloTimeout):
		return status.Error(codes.DeadlineExceeded, "no Hello received")
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	sess := &session{nodeID: nodeID, cancel: cancel, out: make(chan *agentv1.DashboardMessage, 512)}
	sess.lastMsg.Store(time.Now().UnixNano())
	s.register(sess)
	defer func() {
		s.unregister(sess)
		s.core.NodeDisconnected(nodeID) // publishes the node's current status
	}()

	welcome, err := s.core.NodeConnected(ctx, nodeID, hello)
	if err != nil {
		log.Error("node connected", "err", err)
		return status.Error(codes.Internal, "failed to load desired state")
	}
	sess.mu.Lock()
	sess.out <- &agentv1.DashboardMessage{Msg: &agentv1.DashboardMessage_Welcome{Welcome: welcome}}
	for _, m := range sess.pending {
		select {
		case sess.out <- m:
		default:
		}
	}
	sess.pending, sess.welcomed = nil, true
	sess.mu.Unlock()

	// Sender: the only goroutine calling stream.Send.
	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-sess.out:
				if err := stream.Send(m); err != nil {
					sendErr <- err
					return
				}
			}
		}
	}()

	ping := time.NewTicker(PingInterval)
	defer ping.Stop()
	watchdog := time.NewTicker(OfflineTimeout / 4)
	defer watchdog.Stop()
	var nonce int64
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-recvErr:
			log.Info("stream closed", "err", err)
			return nil
		case err := <-sendErr:
			log.Info("send failed", "err", err)
			return nil
		case <-ping.C:
			nonce++
			s.trySend(sess, &agentv1.DashboardMessage{Msg: &agentv1.DashboardMessage_Ping{Ping: &agentv1.Ping{Nonce: nonce}}})
		case <-watchdog.C:
			if time.Since(time.Unix(0, sess.lastMsg.Load())) > OfflineTimeout {
				log.Warn("node silent, closing stream")
				return status.Error(codes.DeadlineExceeded, "no message within timeout")
			}
		case m := <-recv:
			sess.lastMsg.Store(time.Now().UnixNano())
			s.dispatch(ctx, nodeID, m)
		}
	}
}

func (s *Server) dispatch(ctx context.Context, nodeID string, m *agentv1.AgentMessage) {
	switch msg := m.GetMsg().(type) {
	case *agentv1.AgentMessage_Heartbeat:
		s.core.NodeHeartbeat(ctx, nodeID, msg.Heartbeat)
	case *agentv1.AgentMessage_DeviceStatus:
		s.core.NodeDeviceStatus(ctx, nodeID, msg.DeviceStatus)
	case *agentv1.AgentMessage_CapabilitiesChanged:
		s.core.NodeCapabilitiesChanged(ctx, nodeID, msg.CapabilitiesChanged.GetCapabilities())
	case *agentv1.AgentMessage_Pong, *agentv1.AgentMessage_Hello:
		// Pong only refreshes lastMsg; a repeated Hello is ignored.
	}
}

func (s *Server) register(sess *session) {
	s.mu.Lock()
	old := s.sessions[sess.nodeID]
	s.sessions[sess.nodeID] = sess
	s.mu.Unlock()
	if old != nil {
		old.cancel() // one active stream per node: the new one wins
	}
}

func (s *Server) unregister(sess *session) {
	s.mu.Lock()
	if s.sessions[sess.nodeID] == sess {
		delete(s.sessions, sess.nodeID)
	}
	s.mu.Unlock()
}

// Online implements core.NodeLink.
func (s *Server) Online(nodeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[nodeID]
	return ok
}

// Send implements core.NodeLink.
func (s *Server) Send(nodeID string, m *agentv1.DashboardMessage) bool {
	s.mu.Lock()
	sess := s.sessions[nodeID]
	s.mu.Unlock()
	if sess == nil {
		return false
	}
	return s.trySend(sess, m)
}

// trySend queues a message. If the queue is full the stream is reset: the
// agent reconnects and receives the full desired state in the Welcome.
func (s *Server) trySend(sess *session, m *agentv1.DashboardMessage) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.welcomed {
		sess.pending = append(sess.pending, m)
		return true
	}
	select {
	case sess.out <- m:
		return true
	default:
		s.log.Warn("send queue full, resetting stream", "node", sess.nodeID)
		sess.cancel()
		return false
	}
}

// Disconnect implements core.NodeLink.
func (s *Server) Disconnect(nodeID string) {
	s.mu.Lock()
	sess := s.sessions[nodeID]
	s.mu.Unlock()
	if sess != nil {
		sess.cancel()
	}
}
