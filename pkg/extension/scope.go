package extension

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// Scope is what one call may reach, as the subsystem that owns the type of its
// asset resolved it: an extension asset type's by the asset-type side
// (internal/extreg, internal/service/extension_svc), a database asset's by the
// database side. The runtime decides nothing about assets itself; it admits each
// host call against the scope and the manifest's permissions (capHost):
//
//   - a network.http allowlist target is dialed directly, as without a scope;
//   - one of Endpoints is reachable with network.assetEndpoint, along Route with
//     TLS and Auth;
//   - any other address is reachable with network.assetRoute when the scope has
//     a Route, along it by address (a direct route refuses loopback and
//     link-local targets);
//   - auth.kerberos_ticket answers from Tickets with permissions.kerberos;
//   - with Ops set, no other host call runs at all.
//
// A call either has its scope bound when it starts (a database driver session:
// complete, Ops included, never failing) or resolves it through the host's
// ScopeResolver on its first network or ticket host call, and keeps it for the
// rest of the call.
type Scope struct {
	// Asset is the asset the call runs against; nil when it has none.
	Asset *AssetRef
	// Endpoints are the asset's own addresses.
	Endpoints []Endpoint
	// Route is the asset's connection path; nil when the call has none.
	Route *Route
	// TLS is the asset's TLS settings, applied to a connection to one of
	// Endpoints; nil = none.
	TLS *tls.Config
	// Fingerprint identifies the connection settings Route and TLS were built
	// from: it changes whenever they change, and keys the asset's cached HTTP
	// client (see DefaultHostProvider.httpClients).
	Fingerprint string
	// Auth renders the credentials injected into a request to one of
	// Endpoints; nil = none.
	Auth RequestAuth
	// Tickets issues the asset's Kerberos tickets; nil = the asset has none.
	Tickets TicketSource
	// Ops lists the host calls the call may make (op names, io.open narrowed
	// per stream type as IOOpenOp gives them); nil = every host call. Only a
	// scope bound when the call starts restricts ops: a ScopeResolver's scope
	// must leave it nil, since the host calls made before it was resolved were
	// not restricted.
	Ops []string
}

// The host calls a guest can make, as Scope.Ops names them.
const (
	OpLog            = "log"
	OpKVGet          = "kv.get"
	OpKVSet          = "kv.set"
	OpAssetGetConfig = "asset.get_config" //nolint:gosec // G101: a host call name, not a credential
	OpFileDialog     = "file_dialog"
	OpIOOpen         = "io.open"
	OpIOSetDeadline  = "io.set_deadline"
	OpToolEvent      = "tool.event"
	OpToolShouldStop = "tool.should_stop"
	// OpKerberosTicket answers the raw AP-REQ bytes of a Kerberos ticket.
	OpKerberosTicket = "auth.kerberos_ticket"
)

// IOOpenOp is how Scope.Ops names io.open of one stream type ("file", "http",
// "tcp"): a scope admits only the stream types it lists.
func IOOpenOp(streamType string) string { return OpIOOpen + ":" + streamType }

// allows reports whether the scope admits host call op (for io.open, op is
// already narrowed by IOOpenOp).
func (s *Scope) allows(op string) bool {
	return s.Ops == nil || slices.Contains(s.Ops, op)
}

// Endpoint is one address of an asset. Scheme is empty for a bare host:port,
// which admits both http and https.
type Endpoint struct {
	Scheme, Host, Port string
}

// ParseEndpoint reads a URL ("https://es.internal:9200/base") or a host:port
// ("10.0.0.5:9092"). A value naming no port it can infer names no endpoint.
func ParseEndpoint(value string) (Endpoint, bool) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" {
			return Endpoint{}, false
		}
		scheme := strings.ToLower(u.Scheme)
		port := u.Port()
		if port == "" {
			port = defaultPort(scheme)
		}
		if port == "" {
			return Endpoint{}, false
		}
		return Endpoint{Scheme: scheme, Host: u.Hostname(), Port: port}, true
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return Endpoint{}, false
	}
	return Endpoint{Host: host, Port: port}, true
}

// Route is an asset's connection path. A Route must not be copied once used.
type Route struct {
	// Dial dials addr along the path: through an SSH tunnel to addr itself,
	// its name resolved at the far end of a tunnel, proxy or proxy chain, or
	// directly. Never nil.
	Dial DialContextFunc
	// Direct reports a path with no tunnel or proxy: names resolve on this
	// machine.
	Direct bool

	mu sync.Mutex
	// followed are the addresses other than the asset's endpoints the route has
	// dialed, logged on the first dial of each.
	followed map[string]bool
}

// firstDial records a dial of addr and reports whether it is the route's first.
func (r *Route) firstDial(addr string) bool {
	key := addr
	if host, port, err := net.SplitHostPort(addr); err == nil {
		key = net.JoinHostPort(canonicalHost(host), port)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.followed[key] {
		return false
	}
	if r.followed == nil {
		r.followed = map[string]bool{}
	}
	r.followed[key] = true
	return true
}

// RequestAuth renders the credentials an endpoint request carries, from the
// asset's host-side config: called once per request, so a credential the user
// changes takes effect on the next one. nil values mean no credentials. Its
// result never reaches the guest.
type RequestAuth func(ctx context.Context) ([]AuthValue, error)

// AuthValue is one credential injected into a request: In is AuthInHeader (Name
// is the header), AuthInQuery (Name is the parameter) or AuthInBasic (Value is
// "user:password", sent as Authorization: Basic).
type AuthValue struct {
	In, Name, Value string
}

// TicketSource issues the Kerberos tickets of a scope's asset: the AP-REQ bytes
// for service principal spn. The source applies its own principal rules; the
// runtime only decides whether the call may ask.
type TicketSource interface {
	Ticket(ctx context.Context, spn string) ([]byte, error)
}

// ScopeResolver resolves the scope of a call extension ext makes on asset a —
// a stored asset, or an ad-hoc one (a.AdHoc, test connection). It must refuse an
// asset whose type ext does not provide. Ops of the scope it returns must be nil.
type ScopeResolver func(ctx context.Context, ext string, a AssetRef) (*Scope, error)

// endpointSet is a scope's endpoints as the gate matches them.
type endpointSet []Endpoint

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
		epScheme := strings.ToLower(ep.Scheme)
		if (epScheme == "" || epScheme == scheme) && canonicalHost(ep.Host) == host && ep.Port == port {
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
		if canonicalHost(ep.Host) == host && ep.Port == port {
			return true
		}
	}
	return false
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
