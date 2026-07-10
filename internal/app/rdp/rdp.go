// Package rdp implements the experimental RDP binder.
package rdp

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/rdp_svc"
	"github.com/opskat/opskat/internal/sshpool"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// LangProvider is implemented by the system binder.
type LangProvider interface {
	Lang() string
}

type RDP struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	service *rdp_svc.Service
}

func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *RDP {
	r := &RDP{appCtx: appCtx, lang: lang}
	r.service = rdp_svc.New(func(event rdp_svc.Event) {
		if r.ctx == nil {
			return
		}
		wailsRuntime.EventsEmit(r.ctx, "rdp:event:"+event.SessionID, event)
	}, pool)
	conntest.Register(asset_entity.AssetTypeRDP, r.testConnection)
	return r
}

func (r *RDP) Startup(ctx context.Context) { r.ctx = ctx }

func (r *RDP) Cleanup() {
	if r.service != nil {
		r.service.CloseAll()
	}
}

func (r *RDP) ConnectRDP(req rdp_svc.ConnectRequest) (string, error) {
	ctx := i18n.Ctx(r.ctx, r.lang.Lang())
	log := logger.Ctx(ctx)
	log.Info("rdp IPC connect start", zap.Int64("assetID", req.AssetID), zap.String("sessionID", req.SessionID))
	sessionID, err := r.service.Connect(ctx, req)
	if err != nil {
		log.Error("rdp IPC connect failed", zap.Int64("assetID", req.AssetID), zap.String("sessionID", req.SessionID), zap.Error(err))
		return "", err
	}
	log.Info("rdp IPC connect end", zap.Int64("assetID", req.AssetID), zap.String("sessionID", sessionID))
	return sessionID, nil
}

func (r *RDP) SendRDPInput(event rdp_svc.InputEvent) error {
	log := logger.Ctx(r.ctx)
	log.Debug("rdp IPC input start", zap.String("sessionID", event.SessionID), zap.String("kind", event.Kind))
	if err := r.service.SendInput(event); err != nil {
		log.Error("rdp IPC input failed", zap.String("sessionID", event.SessionID), zap.String("kind", event.Kind), zap.Error(err))
		return err
	}
	log.Debug("rdp IPC input end", zap.String("sessionID", event.SessionID), zap.String("kind", event.Kind))
	return nil
}

func (r *RDP) ResizeRDP(sessionID string, width, height int) error {
	log := logger.Ctx(r.ctx)
	log.Info("rdp IPC resize start", zap.String("sessionID", sessionID), zap.Int("width", width), zap.Int("height", height))
	if err := r.service.Resize(sessionID, width, height); err != nil {
		log.Error("rdp IPC resize failed", zap.String("sessionID", sessionID), zap.Int("width", width), zap.Int("height", height), zap.Error(err))
		return err
	}
	log.Info("rdp IPC resize end", zap.String("sessionID", sessionID), zap.Int("width", width), zap.Int("height", height))
	return nil
}

func (r *RDP) SetRDPClipboard(sessionID, text string) error {
	log := logger.Ctx(r.ctx)
	log.Info("rdp IPC set clipboard start", zap.String("sessionID", sessionID), zap.Int("textBytes", len(text)))
	if err := r.service.SetClipboard(sessionID, text); err != nil {
		log.Error("rdp IPC set clipboard failed", zap.String("sessionID", sessionID), zap.Int("textBytes", len(text)), zap.Error(err))
		return err
	}
	log.Info("rdp IPC set clipboard end", zap.String("sessionID", sessionID), zap.Int("textBytes", len(text)))
	return nil
}

func (r *RDP) SetRDPClipboardEnabled(sessionID string, enabled bool) error {
	log := logger.Ctx(r.ctx)
	log.Info("rdp IPC set clipboard enabled start", zap.String("sessionID", sessionID), zap.Bool("enabled", enabled))
	if err := r.service.SetClipboardEnabled(sessionID, enabled); err != nil {
		log.Error("rdp IPC set clipboard enabled failed", zap.String("sessionID", sessionID), zap.Bool("enabled", enabled), zap.Error(err))
		return err
	}
	log.Info("rdp IPC set clipboard enabled end", zap.String("sessionID", sessionID), zap.Bool("enabled", enabled))
	return nil
}

func (r *RDP) SetRDPClipboardFilesFromLocal(sessionID string) (int, error) {
	log := logger.Ctx(r.ctx)
	log.Info("rdp IPC set clipboard files start", zap.String("sessionID", sessionID))
	count, err := r.service.SetClipboardFilesFromLocal(sessionID)
	if err != nil {
		log.Error("rdp IPC set clipboard files failed", zap.String("sessionID", sessionID), zap.Error(err))
		return 0, err
	}
	log.Info("rdp IPC set clipboard files end", zap.String("sessionID", sessionID), zap.Int("fileCount", count))
	return count, nil
}

func (r *RDP) CloseRDP(sessionID string) error {
	log := logger.Ctx(r.ctx)
	log.Info("rdp IPC close start", zap.String("sessionID", sessionID))
	if err := r.service.Close(sessionID); err != nil {
		log.Error("rdp IPC close failed", zap.String("sessionID", sessionID), zap.Error(err))
		return err
	}
	log.Info("rdp IPC close end", zap.String("sessionID", sessionID))
	return nil
}
