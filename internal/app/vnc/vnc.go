package vnc

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/vnc_svc"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type VNC struct {
	ctx     context.Context
	manager *vnc_svc.Manager
}

func New(appCtx context.Context, manager *vnc_svc.Manager) *VNC {
	return &VNC{ctx: appCtx, manager: manager}
}

func (r *VNC) Startup(ctx context.Context) {
	r.ctx = ctx
}

func (r *VNC) Cleanup() {
	r.manager.Cleanup()
}

func (r *VNC) ConnectVNC(assetID int64) (*vnc_svc.Session, error) {
	return r.manager.Connect(r.ctx, assetID)
}

// ConnectVNCTemporary opens only the raw transport for an unsaved VNC form.
// The frontend shared noVNC client owns policy negotiation, trust, authentication and ServerInit.
func (r *VNC) ConnectVNCTemporary(configJSON, plainPassword string) (*vnc_svc.Session, error) {
	sessionID := uuid.NewString()
	log := logger.Ctx(r.ctx)
	log.Info("VNC temporary IPC connect start", zap.String("sessionID", sessionID))
	asset := &asset_entity.Asset{Type: asset_entity.AssetTypeVNC, Config: configJSON}
	cfg, err := asset.GetVNCConfig()
	if err != nil {
		wrapped := fmt.Errorf("VNC配置无效: %w", err)
		log.Error("VNC temporary IPC connect failed", zap.String("sessionID", sessionID), zap.Error(wrapped))
		return nil, wrapped
	}
	session, err := r.manager.ConnectTemporary(r.ctx, sessionID, cfg, plainPassword)
	if err != nil {
		log.Error("VNC temporary IPC connect failed", zap.String("sessionID", sessionID), zap.Error(err))
		return nil, err
	}
	log.Info("VNC temporary IPC connect end", zap.String("sessionID", sessionID))
	return session, nil
}

func (r *VNC) DisconnectVNC(sessionID string) {
	r.manager.Disconnect(sessionID)
}

func (r *VNC) CheckVNCServerKey(sessionID, publicKeyB64 string) (*vnc_svc.VNCServerKeyCheck, error) {
	logger.Ctx(r.ctx).Info("VNC server key check start", zap.String("sessionID", sessionID))
	check, err := r.manager.CheckVNCServerKey(r.ctx, sessionID, publicKeyB64)
	if err != nil {
		logger.Ctx(r.ctx).Error("VNC server key check failed", zap.String("sessionID", sessionID), zap.Error(err))
		return nil, err
	}
	logger.Ctx(r.ctx).Info("VNC server key check end",
		zap.String("sessionID", sessionID), zap.String("state", string(check.State)),
		zap.String("host", check.Host), zap.Int("port", check.Port))
	return check, nil
}

func (r *VNC) TrustVNCServerKey(sessionID, publicKeyB64 string, replace bool) error {
	logger.Ctx(r.ctx).Info("VNC server key trust start", zap.String("sessionID", sessionID), zap.Bool("replace", replace))
	if err := r.manager.TrustVNCServerKey(r.ctx, sessionID, publicKeyB64, replace); err != nil {
		logger.Ctx(r.ctx).Error("VNC server key trust failed", zap.String("sessionID", sessionID), zap.Bool("replace", replace), zap.Error(err))
		return err
	}
	logger.Ctx(r.ctx).Info("VNC server key trust end", zap.String("sessionID", sessionID), zap.Bool("replace", replace))
	return nil
}

// StartVNCStream 挂上 IPC 回调并启动读 pump。前端必须在 EventsOn 订阅
// vnc:data/closed 之后再调,保证不丢 RFB 握手首包。
func (r *VNC) StartVNCStream(sessionID string) error {
	return r.manager.SetCallbacks(
		sessionID,
		func(data []byte) {
			wailsRuntime.EventsEmit(r.ctx, "vnc:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
		},
		func() {
			wailsRuntime.EventsEmit(r.ctx, "vnc:closed:"+sessionID, nil)
		},
	)
}

// WriteVNC 把前端(noVNC)发来的 base64 字节写入目标连接。
func (r *VNC) WriteVNC(sessionID, dataB64 string) error {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("解码远程桌面数据失败: %w", err)
	}
	return r.manager.Write(sessionID, data)
}

func (r *VNC) EncodeVNCClipboardText(text string) ([]int, error) {
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("VNC 剪贴板文本无法使用 GBK 编码: %w", err)
	}
	result := make([]int, len(encoded))
	for i, value := range encoded {
		result[i] = int(value)
	}
	return result, nil
}
