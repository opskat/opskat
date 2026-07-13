package remote_desktop

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/remote_desktop_svc"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type LangProvider interface {
	Lang() string
}

type RemoteDesktop struct {
	ctx     context.Context
	lang    LangProvider
	manager *remote_desktop_svc.Manager
}

func New(appCtx context.Context, lang LangProvider, manager *remote_desktop_svc.Manager) *RemoteDesktop {
	if manager == nil {
		manager = remote_desktop_svc.NewManager(nil)
	}
	r := &RemoteDesktop{ctx: appCtx, lang: lang, manager: manager}
	conntest.Register("vnc", r.testVNCConnection)
	return r
}

func (r *RemoteDesktop) Startup(ctx context.Context) {
	r.ctx = ctx
}

func (r *RemoteDesktop) Cleanup() {
	r.manager.Cleanup()
	conntest.Unregister("vnc")
}

func (r *RemoteDesktop) ConnectRemoteDesktop(assetID int64) (*remote_desktop_svc.Session, error) {
	return r.manager.Connect(r.ctx, assetID, remote_desktop_svc.ConnectOptions{})
}

func (r *RemoteDesktop) DisconnectRemoteDesktop(sessionID string) {
	r.manager.Disconnect(sessionID)
}

func (r *RemoteDesktop) EncodeVNCClipboardText(text string) ([]int, error) {
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

func (r *RemoteDesktop) TestRemoteDesktopConnection(assetType, configJSON string) error {
	if assetType != "vnc" {
		return fmt.Errorf("不支持的远程桌面类型: %s", assetType)
	}
	return r.testVNCConnection(r.ctx, configJSON, "")
}

func (r *RemoteDesktop) testVNCConnection(ctx context.Context, configJSON, plainPassword string) error {
	var cfg asset_entity.VNCConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("VNC配置无效: %w", err)
	}
	password := plainPassword
	if password == "" {
		resolved, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, &cfg)
		if err != nil {
			return err
		}
		password = resolved
	}
	return r.manager.TestConfig(ctx, &cfg, password)
}
