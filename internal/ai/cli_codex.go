package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Codex App Server JSON-RPC 2.0 适配器

// codexJSONRPC JSON-RPC 2.0 消息
type codexJSONRPC struct {
	Method string          `json:"method,omitempty"`
	ID     *int64          `json:"id,omitempty"`     // 请求时设置，通知时不设置
	Params json.RawMessage `json:"params,omitempty"` // 请求参数
	Result json.RawMessage `json:"result,omitempty"` // 响应结果
	Error  *codexRPCError  `json:"error,omitempty"`  // 错误
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CodexAppServer 管理与 codex app-server 的通信
type CodexAppServer struct {
	proc     *CLIProcess
	threadID string
	nextID   atomic.Int64
	mu       sync.Mutex

	// 响应等待
	pending   map[int64]chan codexJSONRPC
	pendingMu sync.Mutex

	// 通知事件分发
	notifyCh chan codexJSONRPC // 后台 reader 将通知事件发到这里
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewCodexAppServer 创建 Codex App Server 客户端
func NewCodexAppServer() *CodexAppServer {
	return &CodexAppServer{
		pending:  make(map[int64]chan codexJSONRPC),
		notifyCh: make(chan codexJSONRPC, 128),
	}
}

// Start 启动 codex app-server 进程并完成初始化握手
func (s *CodexAppServer) Start(ctx context.Context, cliPath string) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	proc, err := StartCLIProcess(s.ctx, cliPath, []string{"app-server"})
	if err != nil {
		return err
	}
	s.proc = proc

	// 启动后台 stdout reader（必须在 sendRequest 之前启动）
	go s.readLoop()

	// 初始化握手
	if err := s.initialize(); err != nil {
		stderrStr := proc.Stderr()
		s.Stop()
		if stderrStr != "" {
			return fmt.Errorf("Codex 初始化失败: %w\nstderr: %s", err, stderrStr)
		}
		return fmt.Errorf("Codex 初始化失败: %w", err)
	}

	return nil
}

// readLoop 后台持续读取 stdout，分发到 pending 响应或 notifyCh
func (s *CodexAppServer) readLoop() {
	lines := s.proc.ReadLines(s.ctx)
	for line := range lines {
		var msg codexJSONRPC
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		// 有 ID 且无 method → 是对请求的响应
		if msg.ID != nil && msg.Method == "" {
			s.pendingMu.Lock()
			if ch, ok := s.pending[*msg.ID]; ok {
				ch <- msg
				delete(s.pending, *msg.ID)
			}
			s.pendingMu.Unlock()
			continue
		}

		// 否则是通知事件，发到 notifyCh
		select {
		case s.notifyCh <- msg:
		case <-s.ctx.Done():
			return
		}
	}
}

// initialize 发送 initialize 请求和 initialized 通知
func (s *CodexAppServer) initialize() error {
	initParams := map[string]any{
		"clientInfo": map[string]any{
			"name":    "codex-cli",
			"version": "1.0.0",
		},
	}
	_, err := s.sendRequest("initialize", initParams)
	if err != nil {
		return err
	}

	// 发送 initialized 通知（无 id）
	return s.sendNotification("initialized", nil)
}

// StartThread 开始新的对话线程
func (s *CodexAppServer) StartThread() error {
	result, err := s.sendRequest("thread/start", map[string]any{})
	if err != nil {
		return err
	}

	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("解析 thread/start 响应失败: %w", err)
	}
	s.mu.Lock()
	s.threadID = resp.Thread.ID
	s.mu.Unlock()
	return nil
}

// SendTurn 发送用户消息开始一个 turn
func (s *CodexAppServer) SendTurn(ctx context.Context, text string, onEvent func(StreamEvent)) error {
	s.mu.Lock()
	threadID := s.threadID
	s.mu.Unlock()

	if threadID == "" {
		if err := s.StartThread(); err != nil {
			return err
		}
		s.mu.Lock()
		threadID = s.threadID
		s.mu.Unlock()
	}

	params := map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": text}},
	}

	_, err := s.sendRequest("turn/start", params)
	if err != nil {
		return err
	}

	// 从 notifyCh 读取事件直到 turn 完成
	for {
		select {
		case msg := <-s.notifyCh:
			done := s.handleNotification(msg.Method, msg.Params, onEvent)
			if done {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handleNotification 处理 Codex 通知事件
func (s *CodexAppServer) handleNotification(method string, params json.RawMessage, onEvent func(StreamEvent)) bool {
	switch method {
	case "item/agentMessage/delta":
		var p struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(params, &p); err == nil && p.Delta != "" {
			onEvent(StreamEvent{Type: "content", Content: p.Delta})
		}

	case "item/started":
		var p struct {
			Item struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"item"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			if p.Item.Type == "command_execution" && p.Item.Command != "" {
				onEvent(StreamEvent{Type: "content", Content: fmt.Sprintf("\n🔧 %s\n", p.Item.Command)})
			}
		}

	case "item/completed":
		var p struct {
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			if p.Item.Type == "agent_message" && p.Item.Text != "" {
				onEvent(StreamEvent{Type: "content", Content: p.Item.Text})
			}
		}

	case "turn/completed":
		return true

	case "turn/failed":
		var p struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			onEvent(StreamEvent{Type: "error", Error: p.Error})
		}
		return true
	}

	return false
}

// sendRequest 发送 JSON-RPC 请求并等待响应
func (s *CodexAppServer) sendRequest(method string, params any) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	paramsData, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	ch := make(chan codexJSONRPC, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	msg := codexJSONRPC{
		Method: method,
		ID:     &id,
		Params: paramsData,
	}
	if err := s.proc.WriteJSON(msg); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("Codex RPC 错误: %s", resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(30 * time.Second):
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		stderrStr := s.proc.Stderr()
		if stderrStr != "" {
			return nil, fmt.Errorf("Codex 请求超时 (%s)\nstderr: %s", method, stderrStr)
		}
		return nil, fmt.Errorf("Codex 请求超时: %s", method)
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// sendNotification 发送 JSON-RPC 通知（无 id）
func (s *CodexAppServer) sendNotification(method string, params any) error {
	var paramsData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsData = data
	}

	msg := codexJSONRPC{
		Method: method,
		Params: paramsData,
	}
	return s.proc.WriteJSON(msg)
}

// Stop 停止 app-server 进程
func (s *CodexAppServer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.proc != nil {
		s.proc.Stop()
	}
}
