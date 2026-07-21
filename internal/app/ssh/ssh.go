// Package ssh 实现 ssh binder：SSH 终端、SFTP、端口转发。共享 sshManager + 三张 pending sync.Map。
package ssh

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/forward_svc"
	"github.com/opskat/opskat/internal/service/sessionid"
	"github.com/opskat/opskat/internal/service/sftp_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/opskat/opskat/internal/service/zmodem_svc"
	"github.com/opskat/opskat/internal/sshpool"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	zmodem         *zmodem_svc.FileBridge
	pool           *sshpool.Pool
	forwardManager *ForwardManager
	forward        *forward_svc.Service

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
	// ZMODEM 文件桥的进度复用 SFTP 同一套 "transfer:progress:<id>" 事件管线。
	// emit 在调用时才读 s.ctx（Startup 注入），传输总发生在 Startup 之后，故安全。
	s.zmodem = zmodem_svc.New(func(p transfer.Progress) {
		wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+p.TransferID, p)
	})
	s.forwardManager = NewForwardManager(&poolDialer{})
	s.forward = forward_svc.New(s.forwardManager)
	conntest.Register(asset_entity.AssetTypeSSH, s.testConnection)
	assetconn.Register("ssh", s.closeAssetConns)
	assetconn.RegisterInvalidator("ssh", s.dropPooledConns)
	return s
}

// closeAssetConns 断开该资产在 SSH 侧的交互式在用连接：终端会话（连带挂在会话上的
// SFTP 客户端）与端口转发。只在资产被删除时广播。
//
// 隧道池不在这里——它是可重拨的缓存，归 dropPooledConns 管，而 CloseAsset 会把两者
// 都跑一遍。
func (s *SSH) closeAssetConns(_ context.Context, assetID int64) error {
	for _, sessionID := range s.manager.CloseAsset(assetID) {
		s.sftp.CleanupSession(sessionID)
	}
	s.forwardManager.CloseAsset(assetID)
	return nil
}

// dropPooledConns 丢弃各协议共用的 SSH 隧道池里属于该资产的条目。
//
// 资产删除和改配置都要丢：数据库/redis 等资产经这台跳板机建隧道，跳板机的地址或口令
// 改了之后，池里那条按旧配置拨出去的隧道还在，下一次连接照旧复用它。丢掉之后下次
// 自动重拨——池本来就是这么用的，所以这里不影响用户开着的终端。
func (s *SSH) dropPooledConns(_ context.Context, assetID int64) error {
	s.pool.Remove(assetID)
	return nil
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
