// Package ssh 实现 ssh binder：SSH 终端、SFTP、端口转发。共享 sshManager + 三张 pending sync.Map。
package ssh

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/service/sessionid"
	"github.com/opskat/opskat/internal/service/sftp_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现，提供当前 UI 语言。
type LangProvider interface {
	Lang() string
}

// SSHConnectEvent SSH 异步连接进度事件
type SSHConnectEvent struct {
	Type        string   `json:"type"`
	Step        string   `json:"step,omitempty"`
	Message     string   `json:"message,omitempty"`
	SessionID   string   `json:"sessionId,omitempty"`
	Error       string   `json:"error,omitempty"`
	AuthFailed  bool     `json:"authFailed,omitempty"`
	ChallengeID string   `json:"challengeId,omitempty"`
	Prompts     []string `json:"prompts,omitempty"`
	Echo        []bool   `json:"echo,omitempty"`

	HostKeyVerifyID string                `json:"hostKeyVerifyId,omitempty"`
	HostKeyEvent    *ssh_svc.HostKeyEvent `json:"hostKeyEvent,omitempty"`
}

// SSH binder：SSH 终端 + SFTP + 端口转发。
type SSH struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider

	manager        *ssh_svc.Manager
	sftp           *sftp_svc.Service
	pool           *sshpool.Pool
	forwardManager *ForwardManager

	connIDGen *sessionid.Generator

	pendingAuthResponses    sync.Map // map[string]chan []string
	pendingHostKeyResponses sync.Map // map[string]chan ssh_svc.HostKeyAction
	pendingConnections      sync.Map // map[string]context.CancelFunc
}

// New 构造 ssh binder。manager/sftp/pool 由 main.go 创建后注入。
func New(appCtx context.Context, lang LangProvider, mgr *ssh_svc.Manager, sftp *sftp_svc.Service, pool *sshpool.Pool) *SSH {
	s := &SSH{
		appCtx:    appCtx,
		lang:      lang,
		manager:   mgr,
		sftp:      sftp,
		pool:      pool,
		connIDGen: sessionid.NewGenerator("conn"),
	}
	s.forwardManager = NewForwardManager(&poolDialer{})
	return s
}

// nextConnectionID 生成跨重启唯一的连接中转 ID(连接中阶段的 tab id),
// 避免与持久化的旧 connecting tab 撞号(issue #141)。
func (s *SSH) nextConnectionID() string {
	return s.connIDGen.Next()
}

// Startup 保存 Wails ctx，方便 EventsEmit 用。
func (s *SSH) Startup(ctx context.Context) { s.ctx = ctx }

// Cleanup ssh manager 由前端通过 Disconnect 主动关；这里只是占位。
func (s *SSH) Cleanup() {}
