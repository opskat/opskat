// Package sshtuning holds the process-wide SSH/TCP connection tuning that is
// applied to every outbound SSH connection — both interactive terminal
// sessions (internal/service/ssh_svc) and pooled connections
// (internal/sshpool). It is a single mutable source of truth (mirroring the
// way sshkeepalive used to expose one global interval constant) so that a user
// changing the settings updates all future connections without re-wiring.
package sshtuning

import (
	"net"
	"sync"
	"time"
)

// Default values used when a setting is left unconfigured. The 30s keepalive is
// deliberately shorter than a "typical" 60s: NAT gateways, home routers and
// proxy chains (e.g. a transparent mihomo/Clash side-router routing SSH through
// an overseas proxy) commonly reap idle TCP connections within 30-60s, and only
// the SSH-level heartbeat traverses the whole proxy chain — so a 60s heartbeat
// races the reaper and the session drops. 30s keeps idle sessions alive in those
// setups; users on an aggressive proxy can lower it further. The 30s heartbeat is
// still negligible traffic (one tiny request/reply per connection per interval).
const (
	DefaultKeepAliveInterval = 30 * time.Second
	DefaultDialTimeout       = 30 * time.Second
)

// Settings is the connection tuning applied to outbound SSH connections.
type Settings struct {
	// TCPNoDelay disables Nagle's algorithm (TCP_NODELAY) on the underlying
	// socket so small interactive writes are sent immediately.
	TCPNoDelay bool
	// TCPKeepAlive enables OS-level TCP keepalive probes (SO_KEEPALIVE).
	TCPKeepAlive bool
	// KeepAliveInterval is the period between SSH-level "keepalive@openssh.com"
	// heartbeats (the empty packets that keep a session alive through idle
	// NAT/firewall timeouts). A value <= 0 disables the heartbeat.
	KeepAliveInterval time.Duration
	// DialTimeout caps the TCP connect phase. A value <= 0 means
	// DefaultDialTimeout.
	DialTimeout time.Duration
}

// Default returns the built-in tuning used before the user configures anything.
func Default() Settings {
	return Settings{
		TCPNoDelay:        true,
		TCPKeepAlive:      true,
		KeepAliveInterval: DefaultKeepAliveInterval,
		DialTimeout:       DefaultDialTimeout,
	}
}

// DialTimeoutOrDefault returns the configured dial timeout, falling back to
// DefaultDialTimeout when unset.
func (s Settings) DialTimeoutOrDefault() time.Duration {
	if s.DialTimeout <= 0 {
		return DefaultDialTimeout
	}
	return s.DialTimeout
}

var (
	mu      sync.RWMutex
	current = Default()
)

// Get returns the current process-wide tuning.
func Get() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Set replaces the process-wide tuning. Future connections pick it up; existing
// connections keep the tuning they were dialed with.
func Set(s Settings) {
	mu.Lock()
	defer mu.Unlock()
	current = s
}

// ResolveKeepAlive returns the effective SSH keepalive heartbeat interval for a
// single connection: a positive per-asset override (in seconds) wins, otherwise
// the process-wide default is used. This is the one place the "override else
// default" precedence lives, so callers pass the raw per-asset seconds (0 =
// inherit) and don't re-implement the fallback.
func ResolveKeepAlive(overrideSeconds int) time.Duration {
	if overrideSeconds > 0 {
		return time.Duration(overrideSeconds) * time.Second
	}
	return Get().KeepAliveInterval
}

// Dialer builds a net.Dialer honoring the timeout and SO_KEEPALIVE settings.
// TCP_NODELAY is NOT a Dialer field — apply it on the resulting *net.TCPConn
// (see ApplyTCPOptions).
func (s Settings) Dialer() *net.Dialer {
	d := &net.Dialer{Timeout: s.DialTimeoutOrDefault()}
	if s.TCPKeepAlive {
		// 0 keeps Go's default keepalive period while still enabling probes.
		d.KeepAlive = 0
	} else {
		// Negative disables keepalive probes entirely.
		d.KeepAlive = -1
	}
	return d
}

// ApplyTCPOptions sets TCP_NODELAY on conn per the tuning. It is a no-op for
// non-TCP connections (e.g. a SOCKS5-proxied or jump-host channel conn), where
// the option is not reachable. Returns the first error encountered, if any.
func (s Settings) ApplyTCPOptions(conn net.Conn) error {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return nil
	}
	return tcp.SetNoDelay(s.TCPNoDelay)
}
