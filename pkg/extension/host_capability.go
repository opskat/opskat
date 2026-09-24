// pkg/extension/host_capability.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// capHost decorates a HostProvider with per-call capability enforcement.
//
// Only OpenIO needs a decision — everything else is already scoped to the
// extension by construction (KV is namespaced per extension; asset config is
// only ever the invocation's own asset, and the provider refuses one whose type
// the extension does not register and applies its credentials capability). Embedding the inner provider
// keeps this file a single middleware instead of a pass-through for every
// method the interface happens to have.
type capHost struct {
	HostProvider
	manifest *Manifest
	extDir   string
}

// NewCapabilityHost wraps inner with capability enforcement.
func NewCapabilityHost(inner HostProvider, manifest *Manifest, extDir string) HostProvider {
	return &capHost{HostProvider: inner, manifest: manifest, extDir: extDir}
}

func (c *capHost) OpenIO(ctx context.Context, asset *AssetRef, params IOOpenParams) (*IOResource, error) {
	switch params.Type {
	case "file":
		switch params.Mode {
		case "read":
			if err := c.manifest.CheckFSRead(params.Path, c.extDir); err != nil {
				return nil, err
			}
		case "write":
			if err := c.manifest.CheckFSWrite(params.Path, c.extDir); err != nil {
				return nil, err
			}
		}
	case "http":
		endpoint, err := c.gateHTTP(asset, &params)
		if err != nil {
			c.logDenied(ctx, asset, params.Type, err)
			return nil, err
		}
		if !endpoint {
			asset = nil
		}
	case "tcp":
		// Without network.assetEndpoint TCP stays ungated: it is reserved for
		// first-party extensions (e.g. Kafka) that predate the capability. Do not
		// treat that path as safe for third-party extensions.
		if !c.manifest.Capabilities.Network.AssetEndpoint {
			asset = nil
			break
		}
		if err := c.gateTCP(asset, params.Addr); err != nil {
			c.logDenied(ctx, asset, params.Type, err)
			return nil, err
		}
	}
	// A network stream reaches the inner provider with the asset only when it
	// targets one of the asset's endpoints: the asset's connection path (tunnel,
	// proxy chain, TLS), its cached keep-alive client and its credentials are how
	// the host talks to that endpoint, and nothing else the same call reaches
	// (an allowlisted public API, an ungated TCP address) may ride them.
	return c.HostProvider.OpenIO(ctx, asset, params)
}

// gateHTTP admits a request that targets one of the asset's endpoints — private
// addresses included, and redirects held to the same endpoints — or else one the
// static allowlist admits, with private reach only through a declared tunnel.
// endpoint reports which of the two admitted it.
func (c *capHost) gateHTTP(asset *AssetRef, params *IOOpenParams) (endpoint bool, err error) {
	endpointScoped := c.manifest.Capabilities.Network.AssetEndpoint && asset != nil
	if endpointScoped {
		eps, err := c.assetEndpoints(asset)
		if err != nil {
			return false, err
		}
		if u, err := url.Parse(params.URL); err == nil && eps.allowURL(u) {
			params.AllowPrivate = true
			if def := c.manifest.AssetTypeDef(asset.Type); def != nil && def.Auth != nil {
				params.Auth = &HTTPAuth{Def: def.Auth, IsEndpoint: eps.allowURL}
			}
			params.RedirectGuard = func(target *url.URL) error {
				if eps.allowURL(target) {
					return nil
				}
				return fmt.Errorf("http redirect denied: %s://%s is not an endpoint of the asset %q", target.Scheme, target.Host, asset.Name)
			}
			return true, nil
		}
	}
	if err := c.manifest.CheckHTTPURL(params.URL, c.manifest.Capabilities.Tunnel); err != nil {
		if endpointScoped {
			return false, fmt.Errorf("http denied: target is not an endpoint of the asset %q, and %w", asset.Name, err)
		}
		return false, err
	}
	// Pass tunnel capability to dial-time guard.
	params.AllowPrivate = c.manifest.Capabilities.Tunnel
	return false, nil
}

// gateTCP admits a connection only to one of the asset's endpoints.
func (c *capHost) gateTCP(asset *AssetRef, addr string) error {
	if asset == nil {
		return fmt.Errorf("tcp denied: this call is not scoped to an asset, so it has no endpoint to connect to")
	}
	eps, err := c.assetEndpoints(asset)
	if err != nil {
		return err
	}
	if !eps.allowAddr(addr) {
		return fmt.Errorf("tcp denied: %q is not an endpoint of the asset %q", addr, asset.Name)
	}
	return nil
}

func (c *capHost) logDenied(ctx context.Context, asset *AssetRef, ioType string, err error) {
	fields := []zap.Field{zap.String("extension", c.manifest.Name), zap.String("type", ioType), zap.Error(err)}
	if asset != nil {
		fields = append(fields, zap.Int64("assetID", asset.ID))
	}
	logger.Ctx(ctx).Warn("extension network target denied", fields...)
}

// readAssetConfig returns asset's guest-visible config: an ad-hoc call's own
// submitted config (see AdHocAssetConfig — there is no database row to read,
// or it must not be trusted over the caller's unsaved edits), or else the
// inner provider's own reader, scoped to assets of this extension.
func (c *capHost) readAssetConfig(asset *AssetRef) (json.RawMessage, error) {
	if asset.AdHoc != nil {
		return asset.AdHoc.Config, nil
	}
	return c.GetAssetConfig(asset.ID)
}

// assetEndpoints reads the endpoints the asset's config names. The config comes
// from the inner provider — the host's own reader, already scoped to assets of
// this extension — so the addresses are the ones the user typed, never the
// guest's.
func (c *capHost) assetEndpoints(asset *AssetRef) (endpointSet, error) {
	def := c.manifest.AssetTypeDef(asset.Type)
	if def == nil {
		return nil, nil
	}
	fields := EndpointFieldsFromSchema(def.ConfigSchema)
	if len(fields) == 0 {
		return nil, nil
	}
	raw, err := c.readAssetConfig(asset)
	if err != nil {
		return nil, fmt.Errorf("read endpoints of asset %q: %w", asset.Name, err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config of asset %q: %w", asset.Name, err)
	}
	var eps endpointSet
	for _, field := range fields {
		var value string
		if json.Unmarshal(cfg[field], &value) != nil {
			continue // absent or not a string: names no endpoint
		}
		if ep, ok := parseEndpoint(value); ok {
			eps = append(eps, ep)
		}
	}
	return eps, nil
}

// assetEndpoint is one address an endpoint field names. scheme is empty for a
// bare host:port, which admits both http and https.
type assetEndpoint struct {
	scheme, host, port string
}

type endpointSet []assetEndpoint

// parseEndpoint reads a URL ("https://es.internal:9200/base") or a host:port
// ("10.0.0.5:9092"). A value naming no port it can infer names no endpoint.
func parseEndpoint(value string) (assetEndpoint, bool) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" {
			return assetEndpoint{}, false
		}
		scheme := strings.ToLower(u.Scheme)
		port := u.Port()
		if port == "" {
			port = defaultPort(scheme)
		}
		if port == "" {
			return assetEndpoint{}, false
		}
		return assetEndpoint{scheme: scheme, host: canonicalHost(u.Hostname()), port: port}, true
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return assetEndpoint{}, false
	}
	return assetEndpoint{host: canonicalHost(host), port: port}, true
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// canonicalHost folds case and spells an IP literal one way, so "ES.internal"
// and "es.internal", or "fd00:0::1" and "fd00::1", name the same host.
func canonicalHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

// allowURL reports whether u targets one of the endpoints: same scheme (any of
// http/https for a bare host:port), host and port.
func (s endpointSet) allowURL(u *url.URL) bool {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = defaultPort(scheme)
	}
	host := canonicalHost(u.Hostname())
	for _, ep := range s {
		if (ep.scheme == "" || ep.scheme == scheme) && ep.host == host && ep.port == port {
			return true
		}
	}
	return false
}

// allowAddr reports whether a host:port dial targets one of the endpoints.
func (s endpointSet) allowAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = canonicalHost(host)
	for _, ep := range s {
		if ep.host == host && ep.port == port {
			return true
		}
	}
	return false
}
