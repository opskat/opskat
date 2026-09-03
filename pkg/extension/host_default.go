// pkg/extension/host_default.go
package extension

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
)

// TunnelDialer dials a TCP address through an SSH tunnel.
type TunnelDialer interface {
	Dial(tunnelAssetID int64, addr string) (net.Conn, error)
}

// Dependency interfaces for DefaultHostProvider
type AssetConfigGetter interface {
	GetAssetConfig(assetID int64) (json.RawMessage, error)
}

type FileDialogOpener interface {
	FileDialog(dialogType string, opts DialogOptions) (string, error)
}

type KVStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
}

type ActionEventHandler interface {
	OnActionEvent(eventType string, data json.RawMessage) error
}

type DefaultHostConfig struct {
	Logger           *zap.Logger
	AssetConfigs     AssetConfigGetter
	FileDialogs      FileDialogOpener
	KV               KVStore
	ActionEvents     ActionEventHandler
	TunnelDialer     TunnelDialer // SSH tunnel dialer (nil = no tunnel support)
	AssetSSHTunnelID int64        // Current asset's SSH tunnel ID (0 = direct)
}

type DefaultHostProvider struct {
	cfg DefaultHostConfig
}

func NewDefaultHostProvider(cfg DefaultHostConfig) *DefaultHostProvider {
	return &DefaultHostProvider{cfg: cfg}
}

func (h *DefaultHostProvider) OpenIO(params IOOpenParams) (*IOResource, error) {
	switch params.Type {
	case "file":
		return OpenFileResource(params.Path, params.Mode)
	case "http":
		var dial DialFunc
		if h.cfg.AssetSSHTunnelID > 0 && h.cfg.TunnelDialer != nil {
			tunnelID := h.cfg.AssetSSHTunnelID
			dial = func(network, addr string) (net.Conn, error) {
				return h.cfg.TunnelDialer.Dial(tunnelID, addr)
			}
		}
		return OpenHTTPResource(params, dial)
	case "tcp":
		return h.openTCP(params)
	default:
		return nil, fmt.Errorf("unknown IO type: %q", params.Type)
	}
}

func (h *DefaultHostProvider) openTCP(params IOOpenParams) (*IOResource, error) {
	if params.Addr == "" {
		return nil, fmt.Errorf("tcp: addr is required")
	}
	timeout := time.Duration(params.Timeout) * time.Millisecond
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	var conn net.Conn
	var err error
	if h.cfg.AssetSSHTunnelID > 0 && h.cfg.TunnelDialer != nil {
		// NOTE: params.Timeout is not honored on the tunnel path — TunnelDialer.Dial
		// uses its own default dial timeout. Callers that need tighter control should
		// set a deadline on the resulting handle via host_io_set_deadline.
		conn, err = h.cfg.TunnelDialer.Dial(h.cfg.AssetSSHTunnelID, params.Addr)
	} else {
		dialer := &net.Dialer{Timeout: timeout}
		conn, err = dialer.Dial("tcp", params.Addr)
	}
	if err != nil {
		return nil, err
	}
	return NewConnResource(conn), nil
}

func (h *DefaultHostProvider) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	if h.cfg.AssetConfigs == nil {
		return nil, fmt.Errorf("asset config getter not configured")
	}
	return h.cfg.AssetConfigs.GetAssetConfig(assetID)
}

func (h *DefaultHostProvider) FileDialog(dialogType string, opts DialogOptions) (string, error) {
	if h.cfg.FileDialogs == nil {
		return "", fmt.Errorf("file dialog opener not configured")
	}
	return h.cfg.FileDialogs.FileDialog(dialogType, opts)
}

func (h *DefaultHostProvider) Log(level, msg string) {
	if h.cfg.Logger == nil {
		return
	}
	switch level {
	case "debug":
		h.cfg.Logger.Debug(msg)
	case "info":
		h.cfg.Logger.Info(msg)
	case "warn":
		h.cfg.Logger.Warn(msg)
	case "error":
		h.cfg.Logger.Error(msg)
	default:
		h.cfg.Logger.Info(msg)
	}
}

func (h *DefaultHostProvider) KVGet(key string) ([]byte, error) {
	if h.cfg.KV == nil {
		return nil, fmt.Errorf("KV store not configured")
	}
	return h.cfg.KV.Get(key)
}

func (h *DefaultHostProvider) KVSet(key string, value []byte) error {
	if h.cfg.KV == nil {
		return fmt.Errorf("KV store not configured")
	}
	return h.cfg.KV.Set(key, value)
}

func (h *DefaultHostProvider) ActionEvent(eventType string, data json.RawMessage) error {
	if h.cfg.ActionEvents == nil {
		return nil
	}
	return h.cfg.ActionEvents.OnActionEvent(eventType, data)
}
