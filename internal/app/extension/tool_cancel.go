package extension

import (
	"context"
	"fmt"
	"sync"
)

// toolCalls tracks the page tool calls in flight by the invocation id the page
// minted for each, so CancelExtensionTool can end one of them. A call is ended by
// canceling its context: that reaches Plugin.CallTool, which interrupts the guest
// and any host IO it is blocked in and gives its instance slot back.
//
// The zero value is ready to use.
type toolCalls struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// begin registers the call named invocationID and returns the context it must
// run under, plus the function that unregisters it once it returns. A second
// call under an id that is still running is refused: cancel could not tell the
// two apart.
func (c *toolCalls) begin(ctx context.Context, invocationID string) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
func (c *toolCalls) cancel(invocationID string) bool {
	c.mu.Lock()
	cancel, ok := c.running[invocationID]
	c.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
