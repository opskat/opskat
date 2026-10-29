package extension

import (
	"sync"
	"sync/atomic"
	"time"
)

// defaultCancelGrace is how long a tool call canceled by invocation id has to
// notice ShouldStop and return on its own before the host cuts it off.
const defaultCancelGrace = 5 * time.Second

// toolCancellation is how one tool call is canceled by its invocation id
// (Plugin.CancelTool). Canceling raises the flag the guest polls through
// ToolContext.ShouldStop at once, so a guest that notices can clean up (abort
// an upload) and return; a guest that does not — it never polls — is ended by
// hardStop, which cancels the call's context, once the grace period runs out.
type toolCancellation struct {
	stopped  atomic.Bool
	hardStop func()

	mu    sync.Mutex
	timer *time.Timer // set by the first cancel
	done  bool        // set by release: the call has returned
}

func newToolCancellation(hardStop func()) *toolCancellation {
	return &toolCancellation{hardStop: hardStop}
}

// cancel raises the stop flag and starts the grace period. Only the first cancel
// does anything; a call that has already returned is left alone.
func (c *toolCancellation) cancel(grace time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done || c.timer != nil {
		return
	}
	c.stopped.Store(true)
	c.timer = time.AfterFunc(grace, c.hardStop)
}

// release marks the call returned, so a pending grace period never fires.
func (c *toolCancellation) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.done = true
	if c.timer != nil {
		c.timer.Stop()
	}
}

func (c *toolCancellation) shouldStop() bool {
	return c.stopped.Load()
}
