package proxychain

import (
	"context"
	"slices"
	"sync"
	"time"
)

// WithDialTimeout bounds the network part of establishing a connection — a TCP
// connect, an SSH hop's handshake — by timeout. Time spent waiting for a person's
// answer (AwaitAnswer on the returned ctx or one derived from it) is not counted,
// so a slow MFA answer on a hop still connects; canceling the parent still ends
// the wait at once. On expiry the ctx is canceled with cause
// context.DeadlineExceeded (read it with context.Cause). 0 means only the caller's
// ctx bounds it.
func WithDialTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	inner, cancel := context.WithCancelCause(ctx)
	c := &dialClock{remaining: timeout, started: time.Now(), cancel: cancel}
	c.timer = time.AfterFunc(timeout, c.expire)
	outer, _ := ctx.Value(dialClocksKey{}).([]*dialClock)
	inner = context.WithValue(inner, dialClocksKey{}, append(slices.Clip(outer), c))
	return inner, c.stop
}

// AwaitAnswer stops every dial timeout on ctx's chain (WithDialTimeout) until the
// returned func is called: the caller is about to wait for a person — an SSH hop's
// MFA prompt answered on a terminal or in the desktop app. Waits may nest or overlap;
// a clock runs again once none is pending.
func AwaitAnswer(ctx context.Context) (resume func()) {
	clocks, _ := ctx.Value(dialClocksKey{}).([]*dialClock)
	for _, c := range clocks {
		c.pause()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, c := range clocks {
				c.resume()
			}
		})
	}
}

type dialClocksKey struct{}

// dialClock is one WithDialTimeout's budget: remaining is what is left of it as of
// started; while paused > 0 the timer is stopped and remaining is frozen.
type dialClock struct {
	mu        sync.Mutex
	remaining time.Duration
	started   time.Time
	paused    int
	done      bool
	timer     *time.Timer
	cancel    context.CancelCauseFunc
}

func (c *dialClock) pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused++
	if c.paused == 1 && !c.done && c.timer.Stop() {
		c.remaining -= time.Since(c.started)
	}
}

func (c *dialClock) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused--
	if c.paused == 0 && !c.done {
		c.started = time.Now()
		c.timer.Reset(max(c.remaining, 0))
	}
}

func (c *dialClock) expire() {
	c.mu.Lock()
	if c.paused > 0 {
		// The timer fired as a wait began: nothing is left of the budget, and the
		// wait's end restarts the timer at zero.
		c.remaining = 0
		c.mu.Unlock()
		return
	}
	c.done = true
	c.mu.Unlock()
	c.cancel(context.DeadlineExceeded)
}

func (c *dialClock) stop() {
	c.mu.Lock()
	c.done = true
	c.timer.Stop()
	c.mu.Unlock()
	c.cancel(context.Canceled)
}
