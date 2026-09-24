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

// AssetConfigGetter reads the config of the assets an extension owns.
type AssetConfigGetter interface {
	// GetAssetConfig returns the config the guest sees through
	// ctx.AssetConfig(): password fields as plaintext or opaque handles, per the
	// extension's credentials capability.
	GetAssetConfig(assetID int64) (json.RawMessage, error)
	// AssetCredentialValues returns the named config fields of assetID as
	// strings, format:"password" fields decrypted whatever the extension's
	// credentials capability — it feeds host-side credential injection
	// (AuthDef) only, and its result must never reach the guest. A field the
	// config lacks is absent from the map; a password field that does not
	// decrypt is an error.
	AssetCredentialValues(ctx context.Context, assetID int64, fields []string) (map[string]string, error)
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
			// gets a single-use client, exactly as the unscoped path above, but
			// still carries whatever credentials resolveAuth rendered.
			client := buildCachedHTTPClient(dial, tlsConfig, params.AllowPrivate, params.RedirectGuard)
			return openHTTPResourceWithClient(params, client, auth)
		}
		client, built := h.httpClients.getOrCreate(asset.ID, fingerprint, func() *http.Client {
			return buildCachedHTTPClient(dial, tlsConfig, params.AllowPrivate, params.RedirectGuard)
		})
		if built {
			logger.Ctx(ctx).Info("extension HTTP client cache miss, built new client",
				zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID))
		}
		return openHTTPResourceWithClient(params, client, auth)
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

// resolveAuth renders the asset's active auth group into the credentials to
// inject; nil when the request carries no auth declaration or no group is
// selected. Any failure to read or decrypt a referenced field fails the open —
// sending the request without the credentials the user configured would be a
// silent downgrade.
func (h *DefaultHostProvider) resolveAuth(ctx context.Context, asset *AssetRef, auth *HTTPAuth) (*requestAuth, error) {
	if auth == nil {
		return nil, nil
	}
	fail := func(err error) (*requestAuth, error) {
		logger.Ctx(ctx).Error("extension credential injection failed",
			zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID), zap.Error(err))
		return nil, fmt.Errorf("credentials of asset %q: %w", asset.Name, err)
	}
	var selected string
	if auth.Def.Selector != "" {
		values, err := h.credentialValues(ctx, asset, []string{auth.Def.Selector})
		if err != nil {
			return fail(err)
		}
		selected = values[auth.Def.Selector]
	}
	group := auth.Def.activeGroup(selected)
	if group == nil {
		logger.Ctx(ctx).Debug("extension asset selects no auth group",
			zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID), zap.String("selector", selected))
		return nil, nil
	}
	values, err := h.credentialValues(ctx, asset, group.fields())
	if err != nil {
		return fail(err)
	}
	logger.Ctx(ctx).Debug("extension credentials injected for endpoint request",
		zap.String("extension", h.cfg.ExtensionName), zap.Int64("assetID", asset.ID),
		zap.String("group", group.When), zap.Int("bindings", len(group.Bindings)))
	return &requestAuth{bindings: group.render(values), isEndpoint: auth.IsEndpoint}, nil
}

// credentialValues resolves the named config fields of asset as strings: from
// the database for a saved asset, or out of an ad-hoc call's host-only
// Credentials (already plaintext — see AdHocAssetConfig) for a test-connection
// call, which has no database row to decrypt from in the first place.
func (h *DefaultHostProvider) credentialValues(ctx context.Context, asset *AssetRef, fields []string) (map[string]string, error) {
	if asset.AdHoc != nil {
		return adHocFieldValues(asset.AdHoc.Credentials, fields), nil
	}
	return h.cfg.AssetConfigs.AssetCredentialValues(ctx, asset.ID, fields)
}

// adHocFieldValues reads fields out of config as strings, exactly as
// AssetCredentialValues does for a stored asset, minus the decrypt step: an
// ad-hoc call's Credentials carry their password fields already resolved to
// plaintext by the caller that built the AdHocAssetConfig. A field absent from
// config is omitted, not "".
func adHocFieldValues(config json.RawMessage, fields []string) map[string]string {
	values := make(map[string]string, len(fields))
	var cfg map[string]json.RawMessage
	if json.Unmarshal(config, &cfg) != nil {
		return values
	}
	for _, field := range fields {
		raw, ok := cfg[field]
		if !ok || string(raw) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			s = string(raw) // a number or bool renders as its JSON text
		}
		values[field] = s
	}
	return values
}

// assetDial resolves the connection path and TLS settings of the invocation's
// asset; a nil dial means a direct dial, a nil tlsConfig means no TLS. A path
// that cannot be built fails the open — falling back to a direct or unverified
// connection would silently bypass what the user configured.
func (h *DefaultHostProvider) assetDial(ctx context.Context, asset *AssetRef) (DialContextFunc, *tls.Config, string, error) {
	if asset == nil || h.cfg.AssetDialer == nil {
		return nil, nil, "", nil
	}
	if asset.AdHoc != nil {
		dialer, ok := h.cfg.AssetDialer.(AdHocAssetDialer)
		if !ok {
			return nil, nil, "", fmt.Errorf("connection path of asset type %q: host does not support testing an ad-hoc connection", asset.Type)
		}
		dial, tlsConfig, err := dialer.DialContextForConfig(ctx, asset.Type, asset.AdHoc)
		if err != nil {
			logger.Ctx(ctx).Error("extension ad-hoc connection path failed",
				zap.String("assetType", asset.Type), zap.Error(err))
			return nil, nil, "", fmt.Errorf("connection path of asset type %q: %w", asset.Type, err)
		}
		return dial, tlsConfig, "", nil
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
	client.CloseIdleConnections()
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
