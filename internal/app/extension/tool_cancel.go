package extension

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// earlyCancelTTL bounds how long a cancel that found no running call is held for
// the call it may have overtaken. Wails dispatches the two IPC calls within
// moments of each other; a cancel for a call that already returned is never
// claimed and simply expires.
const earlyCancelTTL = time.Minute

// toolCalls tracks the page tool calls in flight by the invocation id the page
// minted for each, so CancelExtensionTool can end one of them. A call is ended by
// canceling its context: that reaches Plugin.CallTool, which interrupts the guest
// and any host IO it is blocked in and gives its instance slot back.
//
// Wails serves each IPC call on its own goroutine, so a cancel can arrive before
// the call it names has begun. Such a cancel is remembered (canceled) and the
// call is refused when it does begin: the page has already been told it was
// aborted.
//
// The zero value is ready to use.
type toolCalls struct {
	mu       sync.Mutex
	running  map[string]context.CancelFunc
	canceled map[string]time.Time
}

// begin registers the call named invocationID and returns the context it must
// run under, plus the function that unregisters it once it returns. A second
// call under an id that is still running is refused: cancel could not tell the
// two apart. A call whose cancel already arrived is refused too.
func (c *toolCalls) begin(ctx context.Context, invocationID string) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, canceled := c.canceled[invocationID]; canceled {
		delete(c.canceled, invocationID)
		return nil, nil, fmt.Errorf("tool invocation %q was canceled before it started", invocationID)
	}
	if _, exists := c.running[invocationID]; exists {
		return nil, nil, fmt.Errorf("tool invocation %q is already running", invocationID)
	}
	if c.running == nil {
		c.running = make(map[string]context.CancelFunc)
	}
	ctx, cancel := context.WithCancel(ctx)
	c.running[invocationID] = cancel
	end := func() {
		c.mu.Lock()
		delete(c.running, invocationID)
		c.mu.Unlock()
		cancel()
	}
	return ctx, end, nil
}

// cancel ends the call named invocationID and reports whether one was running.
// When none was, the id is held for earlyCancelTTL so the call, should it begin
// late, is refused.
func (c *toolCalls) cancel(invocationID string) bool {
	c.mu.Lock()
	cancel, ok := c.running[invocationID]
	if !ok {
		now := time.Now()
		for id, at := range c.canceled {
			if now.Sub(at) > earlyCancelTTL {
				delete(c.canceled, id)
			}
		}
		if c.canceled == nil {
			c.canceled = make(map[string]time.Time)
		}
		c.canceled[invocationID] = now
	}
	c.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
