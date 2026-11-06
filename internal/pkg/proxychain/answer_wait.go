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
// the wait at once. On expiry the ctx ends with context.DeadlineExceeded, like a
// context.WithTimeout one, so a dial reading ctx.Err() reports a timeout rather
// than a canceled call. 0 means only the caller's ctx bounds it.
func WithDialTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	c := &dialClock{parent: ctx, done: make(chan struct{}), remaining: timeout, started: time.Now()}
	outer, _ := ctx.Value(dialClocksKey{}).([]*dialClock)
	c.clocks = append(slices.Clip(outer), c)
	c.mu.Lock()
	c.timer = time.AfterFunc(timeout, c.expire)
	c.mu.Unlock()
	c.stopParent = context.AfterFunc(ctx, func() { c.end(ctx.Err()) })
	if err := ctx.Err(); err != nil {
		// Already ended: end now, as context.WithTimeout would, not whenever
		// AfterFunc's goroutine gets to run.
		c.end(err)
	}
	return c, c.stop
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

// dialClock is the ctx WithDialTimeout returns, and its budget: remaining is what
// is left of it as of started; while paused > 0 the timer is stopped and remaining
// is frozen. It is its own context.Context rather than a context.WithCancelCause:
// that one's Err() is always Canceled, which net.Dialer, TLS and SSH handshakes
// would report as a canceled call instead of a timeout. Its Done channel is its own,
// so a ctx derived from it takes its Err() — DeadlineExceeded on expiry.
type dialClock struct {
	parent     context.Context
	clocks     []*dialClock // this clock and the ones it is nested in, outermost first
	done       chan struct{}
	stopParent func() bool

	mu        sync.Mutex
	err       error
	remaining time.Duration
	started   time.Time
	paused    int
	timer     *time.Timer
}

func (c *dialClock) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *dialClock) Done() <-chan struct{}       { return c.done }

func (c *dialClock) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *dialClock) Value(key any) any {
	if key == (dialClocksKey{}) {
		return c.clocks
	}
	return c.parent.Value(key)
}

func (c *dialClock) pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused++
	if c.paused == 1 && c.err == nil && c.timer.Stop() {
		c.remaining -= time.Since(c.started)
	}
}

func (c *dialClock) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused--
	if c.paused == 0 && c.err == nil {
		c.started = time.Now()
		c.timer.Reset(max(c.remaining, 0))
	}
}

func (c *dialClock) expire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused > 0 {
		// The timer fired as a wait began: nothing is left of the budget, and the
		// wait's end restarts the timer at zero.
		c.remaining = 0
		return
	}
	c.endLocked(context.DeadlineExceeded)
}

// end ends the ctx with err; only the first call counts.
func (c *dialClock) end(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endLocked(err)
}

func (c *dialClock) endLocked(err error) {
	if c.err != nil {
		return
	}
	c.err = err
	c.timer.Stop()
	close(c.done)
}

func (c *dialClock) stop() {
	c.stopParent()
	c.end(context.Canceled)
}
