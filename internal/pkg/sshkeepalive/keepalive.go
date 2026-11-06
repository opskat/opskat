// Package sshkeepalive runs an OpenSSH-style keepalive heartbeat over an
// ssh.Client (or any compatible Pinger), so long-lived SSH sessions don't
// get reaped by NAT/firewall idle timeouts.
package sshkeepalive

import (
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// Pinger is the subset of *ssh.Client used by the heartbeat: it sends the
// keepalive global requests and closes the connection once a reply is overdue.
// Defining it as an interface keeps this package decoupled from net/ssh and
// makes it trivial to test with a fake.
type Pinger interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
	Close() error
}

// missedRepliesBeforeClose mirrors OpenSSH's default ServerAliveCountMax: a
// reply that has not arrived within this many intervals means the peer is gone.
const missedRepliesBeforeClose = 3

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
// A request whose reply has not arrived within 3 intervals (OpenSSH's default
// ServerAliveCountMax) means the peer or the path to it is gone: the heartbeat
// closes p, so the client's read loop ends and everything waiting on the
// connection fails through the existing close path instead of hanging.
// If SendRequest returns an error, the goroutine exits without closing p — the
// client's read loop already surfaces that failure.
func Start(p Pinger, interval time.Duration) (stop func()) {
	if interval <= 0 {
		return func() {}
	}

	ticker := time.NewTicker(interval)
	return start(p, ticker.C, ticker.Stop, missedRepliesBeforeClose*interval)
}

// start owns the heartbeat lifecycle for an already-created tick source. Start
// supplies a time.Ticker in production; tests supply explicit ticks so lifecycle
// and shutdown behavior are verified without wall-clock sleeps.
func start(p Pinger, ticks <-chan time.Time, stopTicker func(), replyTimeout time.Duration) (stop func()) {
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
				if !ping(p, replyTimeout) {
					return
				}
			}
		}
	}()

	return stopFn
}

// ping sends one keepalive and reports whether it was answered. When the reply
// is overdue it closes p and waits for the interrupted request to return, so no
// request outlives the heartbeat goroutine.
func ping(p Pinger, replyTimeout time.Duration) bool {
	replied := make(chan error, 1)
	go func() {
		_, _, err := p.SendRequest("keepalive@openssh.com", true, nil)
		replied <- err
	}()
	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()
	select {
	case err := <-replied:
		return err == nil
	case <-timer.C:
		logger.Default().Warn("ssh keepalive reply timed out, closing connection", zap.Duration("timeout", replyTimeout))
		if err := p.Close(); err != nil {
			logger.Default().Warn("close ssh client after keepalive timeout", zap.Error(err))
		}
		<-replied
		return false
	}
}
