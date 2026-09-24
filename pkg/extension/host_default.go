// pkg/extension/host_default.go
package extension

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
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
	// DialContextFor returns the dial function, TLS config, and a fingerprint of
	// assetID's connection settings. dial is nil when the asset connects
	// directly (no tunnel/proxy chain); tlsConfig is nil when the asset's type
	// does not declare/enable TLS. fingerprint changes whenever the resolved
	// tunnel, proxy chain or TLS settings change — it lets a cached HTTP client
	// (see DefaultHostProvider.httpClients) detect that it was built for a
	// since-changed configuration and must be rebuilt rather than reused. An
	// error means the path cannot be built; it must be returned, never
	// downgraded to a direct or unverified connection.
	DialContextFor(ctx context.Context, assetID int64) (dial DialContextFunc, tlsConfig *tls.Config, fingerprint string, err error)
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
	// ExtensionName scopes this provider's cached HTTP clients (httpClientCache)
	// in the process-wide registry (registerHTTPClientCache / DiscardHostHTTPCache)
	// so the extension lifecycle (internal/service/extension_svc) can release them
	// on disable/uninstall, and a reinstall's new provider can release the version
	// it supersedes. Left empty, the provider still caches per-instance — it is
	// just invisible to that lifecycle, which is fine for callers (mainly tests)
	// that never disable/uninstall/reinstall the extension they build one for.
	ExtensionName string
}

type DefaultHostProvider struct {
	cfg         DefaultHostConfig
	httpClients *httpClientCache
}

func NewDefaultHostProvider(cfg DefaultHostConfig) *DefaultHostProvider {
	cache := newHTTPClientCache(cfg.ExtensionName, cfg.Logger)
	if cfg.ExtensionName != "" {
		registerHTTPClientCache(cfg.ExtensionName, cache)
	}
	return &DefaultHostProvider{cfg: cfg, httpClients: cache}
}

func (h *DefaultHostProvider) OpenIO(ctx context.Context, asset *AssetRef, params IOOpenParams) (*IOResource, error) {
	switch params.Type {
	case "file":
		return OpenFileResource(params.Path, params.Mode)
	case "http":
		dial, tlsConfig, fingerprint, err := h.assetDial(ctx, asset)
		if err != nil {
			return nil, err
		}
		if asset == nil {
			// No asset to key a cache on: build a single-use client exactly as
			// before. The transport has no per-request ctx to hand the dial; the
			// invocation's ctx bounds the handle's lifetime anyway.
			var httpDial DialFunc
			if dial != nil {
				httpDial = func(network, addr string) (net.Conn, error) { return dial(ctx, network, addr) }
			}
			return OpenHTTPResource(params, httpDial, tlsConfig)
		}
		client, built := h.httpClients.getOrCreate(asset.ID, fingerprint, func() *http.Client {
			return buildCachedHTTPClient(dial, tlsConfig, params.AllowPrivate, params.RedirectGuard)
		})
		if built {
			logger.Ctx(ctx).Info("extension HTTP client cache miss, built new client",
				zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID))
		}
		return OpenHTTPResourceWithClient(params, client)
	case "tcp":
		dial, tlsConfig, _, err := h.assetDial(ctx, asset)
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
func (h *DefaultHostProvider) assetDial(ctx context.Context, asset *AssetRef) (DialContextFunc, *tls.Config, string, error) {
	if asset == nil || h.cfg.AssetDialer == nil {
		return nil, nil, "", nil
	}
	dial, tlsConfig, fingerprint, err := h.cfg.AssetDialer.DialContextFor(ctx, asset.ID)
	if err != nil {
		logger.Ctx(ctx).Error("extension asset connection path failed",
			zap.Int64("assetID", asset.ID), zap.String("assetType", asset.Type), zap.Error(err))
		return nil, nil, "", fmt.Errorf("connection path of asset %q: %w", asset.Name, err)
	}
	return dial, tlsConfig, fingerprint, nil
}

// InvalidateAssetHTTPClient drops assetID's cached HTTP client, closing its
// idle connections — called (via internal/assetconn, wired in main.go) when
// the asset's stored config changes or the asset is deleted, so the next open
// dials fresh instead of reusing a connection built for the old configuration.
func (h *DefaultHostProvider) InvalidateAssetHTTPClient(assetID int64) {
	h.httpClients.invalidate(assetID)
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

// httpClientCache caches one *http.Client per asset, keyed by the asset id and
// a fingerprint of the connection settings (SSH tunnel, proxy chain, TLS) that
// determine how it dials — so a client is rebuilt, not reused, once those
// settings change. It belongs to one DefaultHostProvider instance, which
// itself belongs to one loaded extension (see the newHost factory in
// main.go): entries are never shared across extensions, and a fresh load of
// the same extension gets a fresh, empty cache.
type httpClientCache struct {
	extName string
	logger  *zap.Logger

	mu      sync.Mutex
	entries map[int64]cachedHTTPClient
}

type cachedHTTPClient struct {
	fingerprint string
	client      *http.Client
}

func newHTTPClientCache(extName string, l *zap.Logger) *httpClientCache {
	return &httpClientCache{extName: extName, logger: l, entries: make(map[int64]cachedHTTPClient)}
}

// getOrCreate returns the cached client for assetID when its fingerprint still
// matches, or builds (and caches) a new one via build otherwise. The returned
// bool reports whether build ran (a cache miss) — callers use it to decide
// whether the event is worth logging. A fingerprint mismatch means the
// asset's connection settings changed since the cached client was built;
// normally caught earlier by InvalidateAssetHTTPClient, checking it here too
// means a stale client is never handed out even if that call is ever missed.
func (c *httpClientCache) getOrCreate(assetID int64, fingerprint string, build func() *http.Client) (*http.Client, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[assetID]; ok {
		if entry.fingerprint == fingerprint {
			return entry.client, false
		}
		closeIdleHTTPClient(entry.client)
	}
	client := build()
	c.entries[assetID] = cachedHTTPClient{fingerprint: fingerprint, client: client}
	return client, true
}

// invalidate drops and closes assetID's cached client, if any. Returns
// whether there was one.
func (c *httpClientCache) invalidate(assetID int64) bool {
	c.mu.Lock()
	entry, ok := c.entries[assetID]
	if ok {
		delete(c.entries, assetID)
	}
	c.mu.Unlock()
	if ok {
		closeIdleHTTPClient(entry.client)
	}
	return ok
}

// closeAll drops and closes every cached client. Returns whether there was
// anything to close.
func (c *httpClientCache) closeAll() bool {
	c.mu.Lock()
	entries := c.entries
	c.entries = make(map[int64]cachedHTTPClient)
	c.mu.Unlock()
	for _, entry := range entries {
		closeIdleHTTPClient(entry.client)
	}
	if len(entries) > 0 && c.logger != nil {
		c.logger.Info("extension HTTP client cache discarded",
			zap.String("extension", c.extName), zap.Int("entries", len(entries)))
	}
	return len(entries) > 0
}

// closeIdleHTTPClient releases a cached client's pooled connections instead of
// leaving them to expire on the transport's own idle timeout — the client is
// being discarded (invalidated, or superseded by a reinstall/disable), so
// nothing will read from that pool again.
func closeIdleHTTPClient(client *http.Client) {
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

// hostHTTPCacheRegistry tracks the live httpClientCache for each loaded
// extension by name, so the extension lifecycle (internal/service/extension_svc)
// can release cached keep-alive connections on disable/uninstall, and so a
// reinstall's new DefaultHostProvider can release the version it replaces —
// without pkg/extension depending on that layer (DefaultHostProvider's
// dependencies are all injected the other way around, e.g. AssetDialer).
// Same-name registration always supersedes: the loader never keeps two live
// providers under one name, so the superseded one will never be called again,
// matching the invariant internal/assetconn documents for its own registry.
var (
	hostHTTPCacheRegistryMu sync.Mutex
	hostHTTPCacheRegistry   = map[string]*httpClientCache{}
)

func registerHTTPClientCache(extName string, cache *httpClientCache) {
	hostHTTPCacheRegistryMu.Lock()
	old := hostHTTPCacheRegistry[extName]
	hostHTTPCacheRegistry[extName] = cache
	hostHTTPCacheRegistryMu.Unlock()
	if old != nil {
		old.closeAll()
	}
}

// DiscardHostHTTPCache releases extName's cached HTTP clients (closing their
// idle connections) and forgets it. Call this when an extension is disabled or
// uninstalled — nothing will re-register the name afterward, unlike a
// reinstall where the new provider's own construction handles it. Returns
// whether there was a cache to discard.
func DiscardHostHTTPCache(extName string) bool {
	hostHTTPCacheRegistryMu.Lock()
	cache, ok := hostHTTPCacheRegistry[extName]
	delete(hostHTTPCacheRegistry, extName)
	hostHTTPCacheRegistryMu.Unlock()
	if !ok {
		return false
	}
	return cache.closeAll()
}
