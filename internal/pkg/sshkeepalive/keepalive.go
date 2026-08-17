// Package sshkeepalive runs an OpenSSH-style keepalive heartbeat over an
// ssh.Client (or any compatible Pinger), so long-lived SSH sessions don't
// get reaped by NAT/firewall idle timeouts.
package sshkeepalive

import (
	"sync"
	"time"
)

// Pinger is the subset of *ssh.Client used to send keepalive global requests.
// Defining it as an interface keeps this package decoupled from net/ssh and
// makes it trivial to test with a fake.
type Pinger interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
}

// Start launches a goroutine that sends an OpenSSH "keepalive@openssh.com"
// global request on p every interval. It returns a stop function the caller
// MUST invoke when shutting down. stop is idempotent and waits until the
// heartbeat goroutine has exited, so no request can outlive a returned stop.
//
// SendRequest may block on network I/O. Callers closing an SSH connection must
// close the underlying client first (which unblocks SendRequest), then call
// stop to join the heartbeat goroutine.
//
// An interval <= 0 disables the heartbeat: no goroutine is started and the
// returned stop is a no-op. This lets callers honor a "keepalive off" setting
// without special-casing it.
//
// If SendRequest returns an error, the goroutine exits silently. Start does
// NOT close the underlying connection — the read loop on the client will
// detect EOF and surface it through the existing close path.
func Start(p Pinger, interval time.Duration) (stop func()) {
	if interval <= 0 {
		return func() {}
	}

	ticker := time.NewTicker(interval)
	return start(p, ticker.C, ticker.Stop)
}

// start owns the heartbeat lifecycle for an already-created tick source. Start
// supplies a time.Ticker in production; tests supply explicit ticks so lifecycle
// and shutdown behavior are verified without wall-clock sleeps.
func start(p Pinger, ticks <-chan time.Time, stopTicker func()) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	stopFn := func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}

	go func() {
		defer wg.Done()
		defer stopTicker()
		for {
			select {
			case <-done:
				return
			case <-ticks:
				if _, _, err := p.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					return
				}
			}
		}
	}()

	return stopFn
}
