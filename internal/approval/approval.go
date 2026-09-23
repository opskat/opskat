package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/localipc"
	"go.uber.org/zap"
)

// BatchItem 批量执行中的单条操作
type BatchItem struct {
	Type string `json:"type"` // 审批类型，由 permission.ApprovalTypeFor(资产类型) 得出：
	// "exec"|"serial"|"sql"|"redis"|"etcd"|"mongo"|"kafka"|"k8s"|"oss"
	AssetID   int64  `json:"asset_id"`
	AssetName string `json:"asset_name"`
	Command   string `json:"command"`
	// Detail 是这一条的补充说明，供折叠摘要展示"两端基点"一类信息（如 cp 的
	// "opsctl cp <src> → <dst>"）；batch_exec 的命令条目不产出，留空。
	Detail string `json:"detail"`
}

// ApprovalRequest is sent from opsctl to the desktop app.
type ApprovalRequest struct {
	Token       string          `json:"token,omitempty"`      // 认证 token
	Type        string          `json:"type"`                 // "exec"|"cp"|"create"|"update"|"delete"|"grant"|"batch"|"ext_tool"
	CheckType   string          `json:"check_type,omitempty"` // internal permission face; cp keeps read/write direction while Type stays "cp"
	AssetID     int64           `json:"asset_id,omitempty"`
	AssetName   string          `json:"asset_name,omitempty"`
	Command     string          `json:"command,omitempty"`
	Detail      string          `json:"detail"`
	SessionID   string          `json:"session_id,omitempty"`  // 统一 session 标识（审批 session 或 grant session）
	GrantItems  []GrantItem     `json:"grant_items,omitempty"` // type="grant" 时使用
	BatchItems  []BatchItem     `json:"batch_items,omitempty"` // type="batch" 时使用
	Description string          `json:"description,omitempty"` // 授权描述
	Extension   string          `json:"extension,omitempty"`   // type="ext_tool": extension name
	Tool        string          `json:"tool,omitempty"`        // type="ext_tool": tool name
	ToolArgs    json.RawMessage `json:"tool_args,omitempty"`   // type="ext_tool": tool arguments
	MFA         *MFAChallenge   `json:"mfa,omitempty"`         // type="mfa": SSH keyboard-interactive challenge
}

// MFACanceledReason 是桌面端在用户取消 / 关闭 MFA 对话框时回给 opsctl 的
// ApprovalResponse.Reason；opsctl 据此区分「人取消了」与其它拒绝原因。
const MFACanceledReason = "mfa canceled"

// MFAChallenge 是 opsctl 请桌面端代为回答的一轮 SSH keyboard-interactive 挑战，
// 字段与服务器发来的原样一致（名称 / 说明 / 逐条提示与回显标记）。
type MFAChallenge struct {
	Name        string   `json:"name"`
	Instruction string   `json:"instruction"`
	Prompts     []string `json:"prompts"`
	Echo        []bool   `json:"echo"`
}

// GrantItem 授权中的单条操作
type GrantItem struct {
	Type      string `json:"type"` // "exec", "cp", "create", "update"
	AssetID   int64  `json:"asset_id"`
	AssetName string `json:"asset_name"`
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
	Command   string `json:"command"`
	Detail    string `json:"detail"`
}

// ApprovalResponse is sent from the desktop app back to opsctl.
type ApprovalResponse struct {
	Approved       bool        `json:"approved"`
	Reason         string      `json:"reason,omitempty"`
	SessionID      string      `json:"session_id,omitempty"`      // grant 审批返回 / session 标识
	ApproveGrant   bool        `json:"approve_grant,omitempty"`   // 用户选择了"记住并允许"
	MatchedPattern string      `json:"matched_pattern,omitempty"` // session 匹配时命中的规则模式
	EditedItems    []GrantItem `json:"edited_items,omitempty"`    // 用户编辑后的 grant items
	ToolResult     string      `json:"tool_result,omitempty"`     // type="ext_tool": execution result (JSON)
	ToolError      string      `json:"tool_error,omitempty"`      // type="ext_tool": execution error message
	MFAAnswers     []string    `json:"mfa_answers,omitempty"`     // type="mfa": answers in prompt order; never logged
}

// SocketPath returns the approval socket path for the given data directory.
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, "approval.sock")
}

// --- Server ---

// ApprovalHandler processes an approval request and returns a response. ctx ends
// when the requesting client disconnects or the server stops.
type ApprovalHandler func(ctx context.Context, req ApprovalRequest) ApprovalResponse

// Server listens on a local IPC endpoint for approval requests from opsctl.
type Server struct {
	handler   ApprovalHandler
	listener  net.Listener
	done      chan struct{}
	wg        sync.WaitGroup
	authToken string // 认证 token，非空时校验
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	stopping  bool
	stopOnce  sync.Once
	active    atomic.Int64
}

// NewServer creates a new approval server.
func NewServer(handler ApprovalHandler, authToken string) *Server {
	return &Server{
		handler:   handler,
		done:      make(chan struct{}),
		authToken: authToken,
		conns:     make(map[net.Conn]struct{}),
	}
}

// Start begins listening at the platform-specific endpoint for socketPath.
func (s *Server) Start(socketPath string) error {
	listener, err := localipc.Listen(socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	s.listener = listener

	s.wg.Add(1)
	go s.acceptLoop()

	logger.Default().Info("approval server listening", zap.Stringer("address", listener.Addr()))
	return nil
}

// Stop prevents new clients and interrupts every accepted connection. It does
// not wait for request handlers, so application shutdown can never be held up
// by a remote client or an in-flight handler.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		if s.listener != nil {
			if err := s.listener.Close(); err != nil {
				logger.Default().Warn("close listener", zap.Error(err))
			}
		}
		s.mu.Lock()
		s.stopping = true
		for conn := range s.conns {
			if err := conn.Close(); err != nil {
				logger.Default().Warn("close active client connection", zap.Error(err))
			}
		}
		s.mu.Unlock()
	})
}

// ActiveRequests returns authenticated approvals that have started and would
// be interrupted by shutting down the desktop app.
func (s *Server) ActiveRequests() int { return int(s.active.Load()) }

func (s *Server) acceptLoop() {
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

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close client connection", zap.Error(err))
		}
	}()

	var req ApprovalRequest
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&req); err != nil {
		resp := ApprovalResponse{Approved: false, Reason: "invalid request"}
		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			logger.Default().Warn("encode error response", zap.Error(err))
		}
		return
	}

	// 校验认证 token
	if s.authToken != "" && req.Token != s.authToken {
		resp := ApprovalResponse{Approved: false, Reason: "authentication failed"}
		if err := json.NewEncoder(conn).Encode(resp); err != nil {
			logger.Default().Warn("encode auth error response", zap.Error(err))
		}
		return
	}
	s.active.Add(1)
	defer s.active.Add(-1)

	// 请求方只写一个请求就等待响应；此后连接上的任何读返回错误（EOF / 被关闭）都
	// 意味着请求方已离开（进程退出、Ctrl-C）或服务端正在关停。据此结束 handler 的
	// ctx，让等人操作的桌面弹窗随之关闭，而不是对着已经不在的调用方挂起。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		cancel()
	}()

	resp := s.handler(ctx, req)
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		logger.Default().Warn("encode approval response", zap.Error(err))
	}
}

// --- Client ---

// SendNotification sends a data-change notification to the desktop app (fire-and-forget).
// resource indicates what changed, e.g. "asset".
func SendNotification(socketPath, token, resource string) {
	if _, err := RequestApprovalWithToken(socketPath, token, ApprovalRequest{
		Type:   "notify",
		Detail: resource,
	}); err != nil {
		logger.Default().Warn("send notification", zap.String("resource", resource), zap.Error(err))
	}
}

// RequestApprovalWithToken connects to the local IPC endpoint and sends an approval request with auth token.
func RequestApprovalWithToken(socketPath, token string, req ApprovalRequest) (ApprovalResponse, error) {
	req.Token = token
	return RequestApproval(socketPath, req)
}

// RequestApproval connects to the local IPC endpoint and sends an approval request.
// Blocks until a response is received.
func RequestApproval(socketPath string, req ApprovalRequest) (ApprovalResponse, error) {
	conn, err := localipc.Dial(socketPath)
	if err != nil {
		return ApprovalResponse{}, fmt.Errorf("cannot connect to desktop app (is it running?): %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close request connection", zap.Error(err))
		}
	}()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return ApprovalResponse{}, fmt.Errorf("send request: %w", err)
	}

	var resp ApprovalResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return ApprovalResponse{}, fmt.Errorf("read response: %w", err)
	}

	return resp, nil
}
