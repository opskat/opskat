// pkg/extension/host_default.go
package extension

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// DialContextFunc opens a network connection, like net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// AssetDialer resolves the host-owned connection path (SSH tunnel, proxy chain,
// TLS) to an asset's endpoint. An implementation is scoped to one extension and
// refuses an asset whose type that extension does not register.
type AssetDialer interface {
	// DialContextFor returns the dial function and TLS config for assetID's
	// connection settings. dial is nil when the asset connects directly (no
	// tunnel/proxy chain); tlsConfig is nil when the asset's type does not
	// declare/enable TLS. An error means the path cannot be built; it must be
	// returned, never downgraded to a direct or unverified connection.
	DialContextFor(ctx context.Context, assetID int64) (dial DialContextFunc, tlsConfig *tls.Config, err error)
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
		dial, tlsConfig, err := h.assetDial(ctx, asset)
		if err != nil {
			return nil, err
		}
		var httpDial DialFunc
		if dial != nil {
			// The transport has no per-request ctx to hand the dial; the
			// invocation's ctx bounds the handle's lifetime anyway.
			httpDial = func(network, addr string) (net.Conn, error) { return dial(ctx, network, addr) }
		}
		return OpenHTTPResource(params, httpDial, tlsConfig)
	case "tcp":
		dial, tlsConfig, err := h.assetDial(ctx, asset)
		if err != nil {
			return nil, err
		}
		return openTCP(ctx, dial, tlsConfig, params, asset)
	default:
		return nil, fmt.Errorf("unknown IO type: %q", params.Type)
	}
}

// assetDial resolves the connection path and TLS settings of the invocation's
// asset; a nil dial means a direct dial, a nil tlsConfig means no TLS. A path
// that cannot be built fails the open — falling back to a direct or unverified
// connection would silently bypass what the user configured.
func (h *DefaultHostProvider) assetDial(ctx context.Context, asset *AssetRef) (DialContextFunc, *tls.Config, error) {
	if asset == nil || h.cfg.AssetDialer == nil {
		return nil, nil, nil
	}
	dial, tlsConfig, err := h.cfg.AssetDialer.DialContextFor(ctx, asset.ID)
	if err != nil {
		logger.Ctx(ctx).Error("extension asset connection path failed",
			zap.Int64("assetID", asset.ID), zap.String("assetType", asset.Type), zap.Error(err))
		return nil, nil, fmt.Errorf("connection path of asset %q: %w", asset.Name, err)
	}
	return dial, tlsConfig, nil
}

func openTCP(ctx context.Context, dial DialContextFunc, tlsConfig *tls.Config, params IOOpenParams, asset *AssetRef) (*IOResource, error) {
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
	if tlsConfig == nil {
		return NewConnResource(conn), nil
	}
	tlsConn, err := wrapTLS(dialCtx, conn, tlsConfig, params.Addr)
	if err != nil {
		fields := []zap.Field{zap.String("addr", params.Addr), zap.Error(err)}
		if asset != nil {
			fields = append(fields, zap.Int64("assetID", asset.ID), zap.String("assetType", asset.Type))
		}
		logger.Ctx(ctx).Error("extension asset TLS handshake failed", fields...)
		return nil, fmt.Errorf("TLS handshake with %q: %w", params.Addr, err)
	}
	return NewConnResource(tlsConn), nil
}

// wrapTLS performs a client TLS handshake over conn, closing it on failure so
// the caller never ends up with a half-open socket. addr's host becomes the
// default ServerName when tlsConfig does not already set one.
func wrapTLS(ctx context.Context, conn net.Conn, tlsConfig *tls.Config, addr string) (net.Conn, error) {
	cfg := tlsConfig.Clone()
	if cfg.ServerName == "" {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			cfg.ServerName = host
		} else {
			cfg.ServerName = addr
		}
	}
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
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
