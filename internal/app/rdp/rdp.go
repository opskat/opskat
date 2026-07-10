// Package rdp implements the experimental RDP binder.
package rdp

import (
	"context"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/rdp_svc"
	"github.com/opskat/opskat/internal/sshpool"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	return r.service.Connect(i18n.Ctx(r.ctx, r.lang.Lang()), req)
}

func (r *RDP) SendRDPInput(event rdp_svc.InputEvent) error {
	return r.service.SendInput(event)
}

func (r *RDP) ResizeRDP(sessionID string, width, height int) error {
	return r.service.Resize(sessionID, width, height)
}

func (r *RDP) SetRDPClipboard(sessionID, text string) error {
	return r.service.SetClipboard(sessionID, text)
}

func (r *RDP) SetRDPClipboardEnabled(sessionID string, enabled bool) error {
	return r.service.SetClipboardEnabled(sessionID, enabled)
}

func (r *RDP) SetRDPClipboardFilesFromLocal(sessionID string) (int, error) {
	return r.service.SetClipboardFilesFromLocal(sessionID)
}

func (r *RDP) CloseRDP(sessionID string) error {
	return r.service.Close(sessionID)
}
