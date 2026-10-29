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

	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/pkg/proxychain"
)

// DialContextFunc opens a network connection, like net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// AssetConfigGetter reads the config of the assets an extension owns.
type AssetConfigGetter interface {
	// GetAssetConfig returns the config the guest sees through
	// ctx.AssetConfig(): password fields as plaintext or opaque handles, per the
	// extension's credentials capability.
	GetAssetConfig(assetID int64) (json.RawMessage, error)
}

type FileDialogOpener interface {
	FileDialog(dialogType string, opts DialogOptions) (string, error)
}

type KVStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
}

type ToolEventHandler interface {
	OnToolEvent(invocationID, eventType string, data json.RawMessage) error
}

type DefaultHostConfig struct {
	Logger       *zap.Logger
	AssetConfigs AssetConfigGetter
	FileDialogs  FileDialogOpener
	KV           KVStore
	ToolEvents   ToolEventHandler
	// Scopes resolves the scope of a call on an asset (endpoints, connection
	// path, credential injection) the first time the call needs it; nil gives
	// every asset an empty scope, which reaches nothing beyond the allowlist.
	Scopes ScopeResolver
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
		if asset == nil {
			if params.Dial != nil {
				// Along an asset's connection path to an address that is not one
				// of its endpoints (network.assetRoute): single-use, no credentials,
				// no TLS settings of the asset's — the route dial itself vets
				// each connection, redirect hops included.
				return openHTTPResourceWithClient(params, buildSingleUseEndpointClient(params.Dial, nil, true), nil)
			}
			// Not an asset endpoint: a direct, single-use client — no connection
			// path to apply and no asset to key a cache on.
			return OpenHTTPResource(params)
		}
		// Credentials are resolved per request and ride on it, never on the
		// cached client: the client outlives this call and serves the asset's
		// requests to any target.
		auth, err := h.resolveAuth(ctx, asset, params.Auth)
		if err != nil {
			return nil, err
		}
		if asset.AdHoc != nil {
			// An ad-hoc call (test connection) will never be asked again the same
			// way — nothing to key a cache entry on that would ever hit — so it
			// gets a single-use client, like the unscoped path above, but still
			// carries whatever credentials resolveAuth rendered.
			client := buildSingleUseEndpointClient(params.Dial, params.TLS, params.AllowPrivate)
			return openHTTPResourceWithClient(params, client, auth)
		}
		client, built := h.httpClients.getOrCreate(asset.ID, params.Fingerprint, func() *http.Client {
			return buildCachedHTTPClient(params.Dial, params.TLS, params.AllowPrivate)
		})
		if built {
			logger.Ctx(ctx).Info("extension HTTP client cache miss, built new client",
				zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID))
		}
		return openHTTPResourceWithClient(params, client, auth)
	case "tcp":
		return openTCP(ctx, params.Dial, params.TLS, params, asset)
	default:
		return nil, fmt.Errorf("unknown IO type: %q", params.Type)
	}
}

// resolveAuth renders the request's credential injection (the scope's Auth);
// nil when the request carries none or the asset selects no credentials. Any
// failure to read or decrypt a referenced field fails the open — sending the
// request without the credentials the user configured would be a silent
// downgrade.
func (h *DefaultHostProvider) resolveAuth(ctx context.Context, asset *AssetRef, auth *HTTPAuth) (*requestAuth, error) {
	if auth == nil {
		return nil, nil
	}
	values, err := auth.Credentials(ctx)
	if err != nil {
		logger.Ctx(ctx).Error("extension credential injection failed",
			zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID), zap.Error(err))
		return nil, fmt.Errorf("credentials of asset %s: %w", assetLabel(asset), err)
	}
	if values == nil {
		logger.Ctx(ctx).Debug("extension asset selects no credentials",
			zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID))
		return nil, nil
	}
	logger.Ctx(ctx).Debug("extension credentials injected for endpoint request",
		zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID), zap.Int("bindings", len(values)))
	return &requestAuth{bindings: values, isEndpoint: auth.IsEndpoint}, nil
}

// resolveScope resolves the scope of a call on asset through the host's
// ScopeResolver (scopeSource). Without one every asset gets an empty scope.
func (h *DefaultHostProvider) resolveScope(ctx context.Context, asset *AssetRef) (*Scope, error) {
	if h.cfg.Scopes == nil {
		return &Scope{Asset: asset}, nil
	}
	return h.cfg.Scopes(ctx, h.cfg.ExtensionName, *asset)
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
		dial = (&netdial.Dialer{}).DialContext
	}
	// The timeout bounds the network, not a person: time spent answering an SSH
	// hop's MFA prompt on the asset's connection path is not counted.
	dialCtx, cancel := proxychain.WithDialTimeout(ctx, timeout)
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

func (h *DefaultHostProvider) ToolEvent(invocationID, eventType string, data json.RawMessage) error {
	if h.cfg.ToolEvents == nil {
		return nil
	}
	return h.cfg.ToolEvents.OnToolEvent(invocationID, eventType, data)
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
	client.CloseIdleConnections()
}

// hostHTTPCacheRegistry tracks the live httpClientCache for each loaded
// extension by name, so the extension lifecycle (internal/service/extension_svc)
// can release cached keep-alive connections on disable/uninstall, and so a
// reinstall's new DefaultHostProvider can release the version it replaces —
// without pkg/extension depending on that layer (DefaultHostProvider's
// dependencies are all injected the other way around, e.g. Scopes).
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
