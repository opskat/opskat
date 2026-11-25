package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/localipc"
)

const maxControlMessageBytes = 1 << 20 // 1 MiB

// Control actions supported over the out-of-band approval control socket.
const (
	ControlActionResolve     = "resolve"
	ControlActionListPending = "list_pending"
	ControlActionGetPending  = "get_pending"
)

// ControlRequest is sent by an external UI host over the local control socket
// (--approval-control-socket) — completely separate from the MCP stdio data plane.
type ControlRequest struct {
	Action string `json:"action"` // "resolve" (default) | "list_pending" | "get_pending"
	Secret string `json:"secret"`
	ResolveRequest
}

// ControlResponse is returned over the local control socket.
type ControlResponse struct {
	OK      bool               `json:"ok"`
	Error   string             `json:"error,omitempty"`
	Result  *TaskResultPayload `json:"result,omitempty"`
	Pause   *PausePayload      `json:"pause,omitempty"`
	Pending []PausePayload     `json:"pending,omitempty"`
}

// ControlServer exposes ExternalApprover resolution over a user-restricted local IPC socket
// (Unix domain socket with 0600 permissions or Windows user-restricted named pipe) so human
// approval decisions never travel on the model-facing MCP stdio channel.
type ControlServer struct {
	approver *ExternalApprover
	listener net.Listener
	done     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	stopping bool
	stopOnce sync.Once
}

// NewControlServer creates an out-of-band control server backed by approver.
func NewControlServer(approver *ExternalApprover) *ControlServer {
	return &ControlServer{
		approver: approver,
		done:     make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}
}

// Start begins listening at the platform-specific local IPC endpoint for socketPath.
func (s *ControlServer) Start(socketPath string) error {
	if socketPath == "" {
		return errors.New("approval control socket path is required in external approval mode")
	}
	listener, err := localipc.Listen(socketPath)
	if err != nil {
		return fmt.Errorf("listen on approval control socket %s: %w", socketPath, err)
	}
	s.listener = listener
	s.wg.Add(1)
	go s.acceptLoop()
	logger.Default().Info("mcp approval control socket listening", zap.Stringer("address", listener.Addr()))
	return nil
}

// Addr returns the platform-specific IPC address the control server is listening on
// (e.g. `\\.\pipe\opskat-<sha256>` on Windows or the Unix domain socket path on Unix).
func (s *ControlServer) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop stops accepting new control connections and closes active ones.
func (s *ControlServer) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.mu.Lock()
		s.stopping = true
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
}

func (s *ControlServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *ControlServer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	var req ControlRequest
	dec := json.NewDecoder(io.LimitReader(conn, maxControlMessageBytes))
	if err := dec.Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(ControlResponse{OK: false, Error: "invalid control request"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	secret := req.Secret
	if secret == "" {
		secret = req.ResolveRequest.Secret
	}

	action := req.Action
	if action == "" {
		action = ControlActionResolve
	}

	switch action {
	case ControlActionListPending:
		pending, err := s.approver.ListPending(ctx, secret)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(ControlResponse{OK: false, Error: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(ControlResponse{OK: true, Pending: pending})

	case ControlActionGetPending:
		pause, err := s.approver.GetPending(ctx, secret, req.ApprovalID)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(ControlResponse{OK: false, Error: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(ControlResponse{OK: true, Pause: pause})

	case ControlActionResolve:
		resolveReq := req.ResolveRequest
		resolveReq.Secret = secret
		res, err := s.approver.Resolve(ctx, resolveReq)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(ControlResponse{OK: false, Error: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(ControlResponse{OK: true, Result: res})

	default:
		_ = json.NewEncoder(conn).Encode(ControlResponse{OK: false, Error: fmt.Sprintf("unknown control action %q", action)})
	}
}

// ResolveViaControlSocket submits a ResolveRequest to an external-mode MCP ControlServer.
func ResolveViaControlSocket(ctx context.Context, socketPath string, req ResolveRequest) (*TaskResultPayload, error) {
	resp, err := sendControlRequest(ctx, socketPath, ControlRequest{
		Action:         ControlActionResolve,
		Secret:         req.Secret,
		ResolveRequest: req,
	})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

// ListPendingViaControlSocket queries pending approval payloads from a ControlServer.
func ListPendingViaControlSocket(ctx context.Context, socketPath, secret string) ([]PausePayload, error) {
	resp, err := sendControlRequest(ctx, socketPath, ControlRequest{
		Action: ControlActionListPending,
		Secret: secret,
	})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return resp.Pending, nil
}

// GetPendingViaControlSocket retrieves the full PausePayload for one approval_id from a ControlServer.
func GetPendingViaControlSocket(ctx context.Context, socketPath, secret, approvalID string) (*PausePayload, error) {
	resp, err := sendControlRequest(ctx, socketPath, ControlRequest{
		Action: ControlActionGetPending,
		Secret: secret,
		ResolveRequest: ResolveRequest{
			ApprovalID: approvalID,
		},
	})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return resp.Pause, nil
}

func sendControlRequest(ctx context.Context, socketPath string, req ControlRequest) (ControlResponse, error) {
	conn, err := localipc.DialContext(ctx, socketPath)
	if err != nil {
		return ControlResponse{}, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer func() {
		close(done)
		_ = conn.Close()
	}()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return ControlResponse{}, err
	}
	var resp ControlResponse
	if err := json.NewDecoder(io.LimitReader(conn, maxControlMessageBytes)).Decode(&resp); err != nil {
		return ControlResponse{}, err
	}
	return resp, nil
}
