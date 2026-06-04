package local

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/localterm_svc"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// LocalConnectRequest 前端本地终端连接请求。
type LocalConnectRequest struct {
	AssetID int64 `json:"assetId"`
	Cols    int   `json:"cols"`
	Rows    int   `json:"rows"`
}

// LocalConnectEvent 本地终端异步连接事件。
type LocalConnectEvent struct {
	Type      string `json:"type"`                // "progress" | "connected" | "error"
	Message   string `json:"message,omitempty"`   // 进度消息
	SessionID string `json:"sessionId,omitempty"` // type=connected 时返回的会话ID
	Error     string `json:"error,omitempty"`     // type=error 时的错误信息
}

// ConnectLocalAsync 异步启动本地终端，立即返回 connectionId。
func (l *Local) ConnectLocalAsync(req LocalConnectRequest) (string, error) {
	logger.Ctx(l.ctx).Info("local terminal connect requested", zap.Int64("assetID", req.AssetID))

	asset, err := asset_svc.Asset().Get(i18n.Ctx(l.ctx, l.lang.Lang()), req.AssetID)
	if err != nil {
		logger.Ctx(l.ctx).Warn("local connect: asset not found", zap.Int64("assetID", req.AssetID), zap.Error(err))
		return "", fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "资产不存在", "asset not found"), err)
	}
	if !asset.IsLocal() {
		logger.Ctx(l.ctx).Warn("local connect: asset is not local type", zap.Int64("assetID", req.AssetID))
		return "", fmt.Errorf("%s", i18n.Pick(l.lang.Lang(), "资产不是本地终端类型", "asset is not a local type"))
	}
	cfg, err := asset.GetLocalConfig()
	if err != nil {
		logger.Ctx(l.ctx).Warn("local connect: parse config failed", zap.Int64("assetID", req.AssetID), zap.Error(err))
		return "", fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "解析本地终端配置失败", "parse local config failed"), err)
	}

	// 生成 connectionId
	connectionID := l.nextConnectionID()
	eventName := "local:connect:" + connectionID

	// 从 l.ctx 派生取消上下文：应用退出（Wails ctx 取消）时连接中途终止，
	// 避免在 CleanAll 之后才生成、逃出关闭范围的孤儿会话。
	connCtx, cancel := context.WithCancel(l.ctx)

	emitEvent := func(event LocalConnectEvent) {
		wailsRuntime.EventsEmit(l.ctx, eventName, event)
	}

	go func() {
		defer cancel()

		if connCtx.Err() != nil {
			return
		}
		emitEvent(LocalConnectEvent{Type: "progress", Message: i18n.Pick(l.lang.Lang(), "正在启动本地终端...", "Starting local terminal...")})

		sessionID, err := l.manager.Connect(localterm_svc.ConnectConfig{
			AssetID: req.AssetID,
			Shell:   cfg.Shell,
			Args:    cfg.Args,
			Cwd:     cfg.Cwd,
			Cols:    req.Cols,
			Rows:    req.Rows,
		})
		if err != nil {
			logger.Ctx(l.ctx).Error("local connect: start session failed",
				zap.Int64("assetID", req.AssetID), zap.String("connID", connectionID), zap.Error(err))
			emitEvent(LocalConnectEvent{Type: "error", Error: err.Error()})
			return
		}
		if connCtx.Err() != nil {
			l.manager.Disconnect(sessionID)
			return
		}

		// 设置回调（sessionID 已知）
		l.manager.SetCallbacks(
			sessionID,
			func(data []byte) {
				wailsRuntime.EventsEmit(l.ctx, "local:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
			},
			func(sid string) {
				wailsRuntime.EventsEmit(l.ctx, "local:closed:"+sid, nil)
			},
		)

		emitEvent(LocalConnectEvent{Type: "connected", SessionID: sessionID})
	}()

	return connectionID, nil
}

// SplitLocal 本地终端分屏：以现有会话相同的 shell 配置新开一个会话，返回新 sessionID。
// 与 SSH 不同，本地没有可复用的连接，分屏即再起一个同配置的 shell PTY。回调接线与
// ConnectLocalAsync 一致（sessionID 已知后再挂，避免首屏输出丢失）。
func (l *Local) SplitLocal(existingSessionID string, cols, rows int) (string, error) {
	sessionID, err := l.manager.SplitFrom(existingSessionID, cols, rows)
	if err != nil {
		logger.Ctx(l.ctx).Warn("local split failed",
			zap.String("existingSessionID", existingSessionID), zap.Error(err))
		return "", fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "本地终端分屏失败", "local terminal split failed"), err)
	}

	l.manager.SetCallbacks(
		sessionID,
		func(data []byte) {
			wailsRuntime.EventsEmit(l.ctx, "local:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
		},
		func(sid string) {
			wailsRuntime.EventsEmit(l.ctx, "local:closed:"+sid, nil)
		},
	)

	logger.Ctx(l.ctx).Info("local terminal split",
		zap.String("from", existingSessionID), zap.String("sessionID", sessionID))
	return sessionID, nil
}

// WriteLocal 向本地终端写入数据（base64 编码）。
func (l *Local) WriteLocal(sessionID string, dataB64 string) error {
	sess, ok := l.manager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("%s: %s", i18n.Pick(l.lang.Lang(), "本地终端会话不存在", "local session not found"), sessionID)
	}

	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "解码数据失败", "decode data failed"), err)
	}

	return sess.Write(data)
}

// ResizeLocalTerminal 调整本地终端尺寸。
func (l *Local) ResizeLocalTerminal(sessionID string, cols, rows int) error {
	sess, ok := l.manager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("%s: %s", i18n.Pick(l.lang.Lang(), "本地终端会话不存在", "local session not found"), sessionID)
	}

	return sess.Resize(cols, rows)
}

// DisconnectLocal 断开本地终端。
func (l *Local) DisconnectLocal(sessionID string) {
	l.manager.Disconnect(sessionID)
}

// ListLocalShells 委托 localterm_svc 探测本机可用 shell（/etc/shells、WSL 发行版等），供前端下拉预设。
func (l *Local) ListLocalShells() ([]localterm_svc.ShellInfo, error) {
	return localterm_svc.DetectShells(), nil
}
