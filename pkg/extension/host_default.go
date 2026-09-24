// pkg/extension/host_default.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// DialContextFunc opens a network connection, like net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// AssetDialer resolves the host-owned connection path (SSH tunnel; later proxy
// chain and TLS) to an asset's endpoint. An implementation is scoped to one
// extension and refuses an asset whose type that extension does not register.
type AssetDialer interface {
	// DialContextFor returns the dial function for assetID's connection
	// settings, nil when the asset connects directly. An error means the path
	// cannot be built; it must be returned, never downgraded to a direct dial.
	DialContextFor(ctx context.Context, assetID int64) (DialContextFunc, error)
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
	OnActionEvent(invocationID, eventType string, data json.RawMessage) error
}

type DefaultHostConfig struct {
	Logger       *zap.Logger
	AssetConfigs AssetConfigGetter
	FileDialogs  FileDialogOpener
	KV           KVStore
	ActionEvents ActionEventHandler
	AssetDialer  AssetDialer // connection path of the invocation's asset (nil = always direct)
}

type DefaultHostProvider struct {
	cfg DefaultHostConfig
}

func NewDefaultHostProvider(cfg DefaultHostConfig) *DefaultHostProvider {
	return &DefaultHostProvider{cfg: cfg}
}

func (h *DefaultHostProvider) OpenIO(ctx context.Context, asset *AssetRef, params IOOpenParams) (*IOResource, error) {
	switch params.Type {
	case "file":
		return OpenFileResource(params.Path, params.Mode)
	case "http":
		dial, err := h.assetDial(ctx, asset)
		if err != nil {
			return nil, err
		}
		var httpDial DialFunc
		if dial != nil {
			// The transport has no per-request ctx to hand the dial; the
			// invocation's ctx bounds the handle's lifetime anyway.
			httpDial = func(network, addr string) (net.Conn, error) { return dial(ctx, network, addr) }
		}
		return OpenHTTPResource(params, httpDial)
	case "tcp":
		dial, err := h.assetDial(ctx, asset)
		if err != nil {
			return nil, err
		}
		return openTCP(ctx, dial, params)
	default:
		return nil, fmt.Errorf("unknown IO type: %q", params.Type)
	}
}

// assetDial resolves the connection path of the invocation's asset; nil means a
// direct dial. A path that cannot be built fails the open — falling back to a
// direct dial would silently bypass the tunnel the user configured.
func (h *DefaultHostProvider) assetDial(ctx context.Context, asset *AssetRef) (DialContextFunc, error) {
	if asset == nil || h.cfg.AssetDialer == nil {
		return nil, nil
	}
	dial, err := h.cfg.AssetDialer.DialContextFor(ctx, asset.ID)
	if err != nil {
		logger.Ctx(ctx).Error("extension asset connection path failed",
			zap.Int64("assetID", asset.ID), zap.String("assetType", asset.Type), zap.Error(err))
		return nil, fmt.Errorf("connection path of asset %q: %w", asset.Name, err)
	}
	return dial, nil
}

func openTCP(ctx context.Context, dial DialContextFunc, params IOOpenParams) (*IOResource, error) {
	if params.Addr == "" {
		return nil, fmt.Errorf("tcp: addr is required")
	}
	timeout := time.Duration(params.Timeout) * time.Millisecond
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dial(dialCtx, "tcp", params.Addr)
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

func (h *DefaultHostProvider) ActionEvent(invocationID, eventType string, data json.RawMessage) error {
	if h.cfg.ActionEvents == nil {
		return nil
	}
	return h.cfg.ActionEvents.OnActionEvent(invocationID, eventType, data)
}
