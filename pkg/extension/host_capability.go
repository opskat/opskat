// pkg/extension/host_capability.go
package extension

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/pkg/netdial"
)

// capHost decorates a HostProvider with per-call permission enforcement: every
// stream and every Kerberos ticket is admitted against the manifest's
// permissions and the call's Scope, which the subsystem owning the call's asset
// resolved (see Scope). It never reads an asset type's declaration itself.
//
// Everything else is already scoped to the extension by construction: KV is
// namespaced per extension, and asset config is only ever the invocation's own
// asset, which the provider refuses unless the extension registers its type and
// serves per its credentials permission. Embedding the inner provider keeps this
// file a single middleware instead of a pass-through for every method the
// interface happens to have.
type capHost struct {
	HostProvider
	manifest *Manifest
	extDir   string
}

// NewCapabilityHost wraps inner with permission enforcement.
func NewCapabilityHost(inner HostProvider, manifest *Manifest, extDir string) HostProvider {
	return &capHost{HostProvider: inner, manifest: manifest, extDir: extDir}
}

// scopeSource is an inner provider that resolves the scope of a call on an
// asset (DefaultHostProvider, through its ScopeResolver). An inner provider
// without one gives every asset an empty scope.
type scopeSource interface {
	resolveScope(ctx context.Context, asset *AssetRef) (*Scope, error)
}

// OpenIO admits a stream. A network stream is admitted against the scope of the
// call ctx belongs to — resolved now if this is the call's first network or
// ticket host call — and reaches the inner provider with the asset only when it
// targets one of the asset's endpoints: the asset's cached client and its
// credentials are how the host talks to that endpoint, and nothing else the same
// call reaches (an allowlisted public API, an address along the asset route) may
// ride them.
func (c *capHost) OpenIO(ctx context.Context, _ *AssetRef, params IOOpenParams) (*IOResource, error) {
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
		return c.HostProvider.OpenIO(ctx, nil, params)
	case "http", "tcp":
		inv, err := invocationFrom(ctx)
		if err != nil {
			return nil, err
		}
		scope, err := c.scopeOf(ctx, inv)
		if err != nil {
			return nil, err
		}
		way, err := c.gateNetwork(inv, scope, &params)
		if err != nil {
			c.logDenied(ctx, inv, scope, params, err)
			return nil, err
		}
		if way == wayDirect {
			// Decided without the asset's connection path, which is never
			// built for it: a path that cannot be built does not fail it.
			return c.HostProvider.OpenIO(ctx, nil, params)
		}
		if err := c.connect(ctx, inv, scope); err != nil {
			return nil, err
		}
		if way == wayEndpoint {
			c.dialEndpoint(scope, &params)
			return c.HostProvider.OpenIO(ctx, scope.Asset, params)
		}
		if scope.Route == nil {
			err := fmt.Errorf("%s denied: the asset %s has no connection path to dial along", params.Type, assetLabel(scope.Asset))
			c.logDenied(ctx, inv, scope, params, err)
			return nil, err
		}
		params.Dial = c.routeDial(inv, scope)
		return c.HostProvider.OpenIO(ctx, nil, params)
	default:
		return c.HostProvider.OpenIO(ctx, nil, params)
	}
}

// connect builds the scope's connection path the first time a stream takes it
// (Scope.Connect). A failure fails that stream and every later one that would
// take the path — never falling back to a direct or unverified connection.
func (c *capHost) connect(ctx context.Context, inv *invocation, scope *Scope) error {
	if err := inv.connectScope(ctx, scope); err != nil {
		logger.Ctx(ctx).Error("extension asset connection path failed",
			zap.String("extension", c.manifest.Name), zap.String("invocation", inv.id),
			zap.Int64("assetID", scope.Asset.ID), zap.String("assetType", scope.Asset.Type), zap.Error(err))
		return fmt.Errorf("connection path of asset %s: %w", assetLabel(scope.Asset), err)
	}
	return nil
}

// streamWay is how gateNetwork admits a stream.
type streamWay int

const (
	// wayDirect: an allowlisted URL, dialed directly.
	wayDirect streamWay = iota
	// wayEndpoint: one of the scope's endpoints, along the asset's route with
	// its TLS settings and credentials.
	wayEndpoint
	// wayRoute: another address, along the asset's route by address.
	wayRoute
)

// scopeOf returns the call's scope, resolving it through the inner provider the
// first time the call needs it. A resolution failure fails this and every later
// network or ticket host call of the call, never anything else.
func (c *capHost) scopeOf(ctx context.Context, inv *invocation) (*Scope, error) {
	return inv.scopeOf(func(asset *AssetRef) (*Scope, error) {
		src, ok := c.HostProvider.(scopeSource)
		if !ok {
			return &Scope{Asset: asset}, nil
		}
		scope, err := src.resolveScope(ctx, asset)
		if err != nil {
			logger.Ctx(ctx).Error("extension call scope resolution failed",
				zap.String("extension", c.manifest.Name), zap.String("invocation", inv.id),
				zap.Int64("assetID", asset.ID), zap.String("assetType", asset.Type), zap.Error(err))
			return nil, fmt.Errorf("scope of asset %s: %w", assetLabel(asset), err)
		}
		logger.Ctx(ctx).Debug("extension call scope resolved",
			zap.String("extension", c.manifest.Name), zap.String("invocation", inv.id),
			zap.Int64("assetID", asset.ID), zap.Int("endpoints", len(scope.Endpoints)),
			zap.Bool("route", scope.Route != nil || scope.Connect != nil), zap.Bool("auth", scope.Auth != nil))
		return scope, nil
	})
}

// gateNetwork decides how a stream may go (streamWay) from the scope's endpoints
// and the manifest's permissions alone — the asset's connection path is not
// needed, and not built, to decide: to one of the scope's endpoints (with
// network.assetEndpoint); to an allowlisted URL directly; or, with
// network.assetRoute, to any other address along the asset's route. Anything else
// is refused, naming why. It sets on params what the decision alone implies.
func (c *capHost) gateNetwork(inv *invocation, scope *Scope, params *IOOpenParams) (streamWay, error) {
	perms := c.manifest.Permissions.Network
	eps := endpointSet(scope.Endpoints)
	toEndpoint := perms.AssetEndpoint && scope.Asset != nil
	alongRoute := perms.AssetRoute && (scope.Route != nil || scope.Connect != nil)
	switch params.Type {
	case "http":
		u, parseErr := url.Parse(params.URL)
		if toEndpoint && parseErr == nil && eps.allowURL(u) {
			// The asset's endpoints are the user's own addresses, private ones
			// included; redirects are held to them.
			params.AllowPrivate = true
			if scope.Auth != nil {
				params.Auth = &HTTPAuth{Credentials: scope.Auth, IsEndpoint: eps.allowURL}
			}
			params.RedirectGuard = func(target *url.URL) error {
				if eps.allowURL(target) {
					return nil
				}
				return fmt.Errorf("http redirect denied: %s://%s is not an endpoint of the asset %s", target.Scheme, target.Host, assetLabel(scope.Asset))
			}
			return wayEndpoint, nil
		}
		allowErr := c.manifest.CheckHTTPURL(params.URL, perms.AllowPrivate)
		if allowErr == nil {
			// Pass the allowPrivate permission to the dial-time guard.
			params.AllowPrivate = perms.AllowPrivate
			return wayDirect, nil
		}
		// An endpoint's own address is never "another address": it is reached
		// as an endpoint or not at all.
		if alongRoute && parseErr == nil && (u.Scheme == "http" || u.Scheme == "https") && !eps.allowAddr(urlHostPort(u)) {
			// The route dial vets every connection itself (routeDial).
			params.AllowPrivate = true
			return wayRoute, nil
		}
		if toEndpoint {
			return wayDirect, fmt.Errorf("http denied: target is not an endpoint of the asset %s, and %w", assetLabel(scope.Asset), allowErr)
		}
		return wayDirect, allowErr
	case "tcp":
		if scope.Asset == nil {
			return wayDirect, fmt.Errorf("tcp denied: this call is not scoped to an asset, so it has no endpoint to connect to %q and no connection path to dial it along", params.Addr)
		}
		isEndpoint := eps.allowAddr(params.Addr)
		if toEndpoint && isEndpoint {
			return wayEndpoint, nil
		}
		if alongRoute && !isEndpoint {
			return wayRoute, nil
		}
		if isEndpoint {
			return wayDirect, fmt.Errorf("tcp denied: %q is an endpoint of the asset %s, but extension %q does not declare permissions.network.assetEndpoint",
				params.Addr, assetLabel(scope.Asset), c.manifest.Name)
		}
		if !perms.AssetRoute {
			return wayDirect, fmt.Errorf("tcp denied: %q is not an endpoint of the asset %s, and extension %q does not declare permissions.network.assetRoute",
				params.Addr, assetLabel(scope.Asset), c.manifest.Name)
		}
		return wayDirect, fmt.Errorf("tcp denied: %q is not an endpoint of the asset %s, which has no connection path to dial it along",
			params.Addr, assetLabel(scope.Asset))
	}
	return wayDirect, fmt.Errorf("unknown IO type: %q", params.Type)
}

// urlHostPort is the host:port a URL dials, its scheme's default port filled in.
func urlHostPort(u *url.URL) string {
	return net.JoinHostPort(u.Hostname(), urlPort(u))
}

// dialEndpoint sets the way to one of the scope's endpoints: the asset's route
// and TLS settings, keyed by their fingerprint.
func (c *capHost) dialEndpoint(scope *Scope, params *IOOpenParams) {
	if scope.Route != nil {
		params.Dial = scope.Route.Dial
	}
	params.TLS = scope.TLS
	params.Fingerprint = scope.Fingerprint
}

// routeDial dials an address other than the asset's endpoints along the
// asset's route, by address: through an SSH tunnel to that address itself, its
// name resolved at the far end of a tunnel, proxy or proxy chain. On a direct
// route the name resolves here, and an address on this machine (loopback,
// unspecified) or on its link (link-local) is refused — an asset the user
// reaches directly cannot point the extension at the user's own machine — unless
// the asset itself is such an address. What is dialed is the address checked,
// not the name resolved again. The route's first dial of each address is logged.
func (c *capHost) routeDial(inv *invocation, scope *Scope) DialContextFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		targets := []string{addr}
		if scope.Route.Direct {
			var err error
			if targets, err = c.directTargets(ctx, inv, scope, network, addr); err != nil {
				return nil, err
			}
		}
		if scope.Route.firstDial(addr) {
			logger.Ctx(ctx).Info("extension dials along the asset's connection path",
				zap.String("extension", c.manifest.Name), zap.String("invocation", inv.id),
				zap.Int64("assetID", scope.Asset.ID), zap.String("addr", addr))
		}
		var errs []error
		for _, target := range targets {
			conn, err := scope.Route.Dial(ctx, network, target)
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, fmt.Errorf("dial %s: %w", addr, errors.Join(errs...))
	}
}

// directTargets are the addresses to dial for addr on a direct route: addr
// itself when it and the asset are both on this machine or its link, else the
// resolved ip:port addresses — none of them on this machine or its link.
func (c *capHost) directTargets(ctx context.Context, inv *invocation, scope *Scope, network, addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolveHere(ctx, network, host)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	if slices.ContainsFunc(ips, onThisMachineOrLink) {
		local, err := c.assetIsLocal(ctx, network, scope)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		if local {
			return []string{addr}, nil
		}
		err = fmt.Errorf("dial denied: %q is a loopback or link-local address, which a direct connection path may only reach when the asset %s itself is on this machine",
			addr, assetLabel(scope.Asset))
		c.logDenied(ctx, inv, scope, IOOpenParams{Type: network, Addr: addr}, err)
		return nil, err
	}
	targets := make([]string, len(ips))
	for i, ip := range ips {
		targets[i] = net.JoinHostPort(ip.String(), port)
	}
	return targets, nil
}

// assetIsLocal reports whether one of the asset's endpoints is on this machine
// or its link.
func (c *capHost) assetIsLocal(ctx context.Context, network string, scope *Scope) (bool, error) {
	for _, ep := range scope.Endpoints {
		ips, err := resolveHere(ctx, network, ep.Host)
		if err != nil {
			return false, fmt.Errorf("resolve the asset's host %s: %w", ep.Host, err)
		}
		if slices.ContainsFunc(ips, onThisMachineOrLink) {
			return true, nil
		}
	}
	return false, nil
}

// resolveHere resolves host on this machine, the way a direct dial would.
func resolveHere(ctx context.Context, network, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return (&netdial.Dialer{}).Resolve(ctx, network, host)
}

// onThisMachineOrLink reports a loopback or link-local address, or the
// unspecified one, which a dial reaches as this machine.
func onThisMachineOrLink(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// kerberosTicket answers auth.kerberos_ticket: only a call whose scope carries a
// ticket source, of an extension declaring permissions.kerberos, gets a ticket —
// and only from that source, whose principal rules apply.
func (c *capHost) kerberosTicket(ctx context.Context, inv *invocation, spn string) ([]byte, error) {
	scope, err := c.scopeOf(ctx, inv)
	if err != nil {
		return nil, err
	}
	var refused error
	switch {
	case scope.Asset == nil:
		refused = errors.New("kerberos ticket refused: this call is not scoped to an asset, so it has no Kerberos ticket source")
	case scope.Tickets == nil:
		refused = fmt.Errorf("kerberos ticket refused: the asset %s has no Kerberos ticket source — only a database asset that logs in with Kerberos has one", assetLabel(scope.Asset))
	case !c.manifest.Permissions.Kerberos:
		refused = fmt.Errorf("kerberos ticket refused: extension %q does not declare permissions.kerberos", c.manifest.Name)
	}
	if refused != nil {
		fields := []zap.Field{zap.String("extension", c.manifest.Name), zap.String("invocation", inv.id), zap.String("spn", spn), zap.Error(refused)}
		if scope.Asset != nil {
			fields = append(fields, zap.Int64("assetID", scope.Asset.ID))
		}
		logger.Ctx(ctx).Warn("extension kerberos ticket refused", fields...)
		return nil, refused
	}
	return scope.Tickets.Ticket(ctx, spn)
}

// logDenied records a refused network target, naming the extension, the asset
// and the target — never the full URL, whose query may carry what the guest
// put there.
func (c *capHost) logDenied(ctx context.Context, inv *invocation, scope *Scope, params IOOpenParams, err error) {
	target := params.Addr
	if params.Type == "http" {
		target = params.URL
		if u, perr := url.Parse(params.URL); perr == nil {
			target = strings.ToLower(u.Scheme) + "://" + u.Host
		}
	}
	fields := []zap.Field{zap.String("extension", c.manifest.Name), zap.String("invocation", inv.id),
		zap.String("type", params.Type), zap.String("target", target), zap.Error(err)}
	if scope.Asset != nil {
		fields = append(fields, zap.Int64("assetID", scope.Asset.ID), zap.String("assetType", scope.Asset.Type))
	}
	logger.Ctx(ctx).Warn("extension network target denied", fields...)
}
