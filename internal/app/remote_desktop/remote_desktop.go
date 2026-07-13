package remote_desktop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/remote_desktop_svc"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	return r.manager.Connect(r.ctx, assetID)
}

func (r *RemoteDesktop) DisconnectRemoteDesktop(sessionID string) {
	r.manager.Disconnect(sessionID)
}

// StartRemoteDesktopStream 挂上 IPC 回调并启动读 pump。前端必须在 EventsOn 订阅
// remote_desktop:data/closed 之后再调,保证不丢 RFB 握手首包。
func (r *RemoteDesktop) StartRemoteDesktopStream(sessionID string) error {
	return r.manager.SetCallbacks(
		sessionID,
		func(data []byte) {
			wailsRuntime.EventsEmit(r.ctx, "remote_desktop:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
		},
		func() {
			wailsRuntime.EventsEmit(r.ctx, "remote_desktop:closed:"+sessionID, nil)
		},
	)
}

// WriteRemoteDesktop 把前端(noVNC)发来的 base64 字节写入目标连接。
func (r *RemoteDesktop) WriteRemoteDesktop(sessionID, dataB64 string) error {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("解码远程桌面数据失败: %w", err)
	}
	return r.manager.Write(sessionID, data)
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
