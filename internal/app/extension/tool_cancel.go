package extension

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/opskat/opskat/pkg/extension"
)

// earlyCancelTTL bounds how long a cancel that found no running call is held for
// the call it may have overtaken. Wails dispatches the two IPC calls within
// moments of each other; a cancel for a call that already returned is never
// claimed and simply expires.
const earlyCancelTTL = time.Minute

// toolCalls tracks the page tool calls in flight by the invocation id the page
// minted for each. It is what CancelExtensionTool ends one of them through, and
// what tells a tool event from a page's call — forwarded to the page — from one
// of an AI exec or opsctl call, which nobody listens to.
//
// A call is canceled by its invocation id in the plugin (Plugin.CancelTool),
// which lets the guest see ShouldStop before cutting it off. Between registering
// here and reaching the runtime the plugin does not know the id yet; a cancel
// landing in that window ends the call's context instead, so it never starts.
//
// The ids are the page's, so they are checked as they enter (checkPageInvocationID):
// the runtime names the calls it makes for AI exec and opsctl in the same id
// space, and a page id shaped like one of those could cancel that call or take
// its id.
//
// Wails serves each IPC call on its own goroutine, so a cancel can arrive before
// the call it names has begun. Such a cancel is remembered (canceled) and the
// call is refused when it does begin: the page has already been told it was
// aborted.
//
// The zero value is ready to use.
type toolCalls struct {
	mu       sync.Mutex
	running  map[string]pageCall
	canceled map[string]time.Time
}

// checkPageInvocationID refuses an id a page may not run or cancel a call under:
// an empty one, and one shaped like the ids the runtime names its own calls by
// (extension.IsRuntimeInvocationID) — those belong to calls the page did not
// make, and keeping page ids out of that shape is what keeps a page cancel to
// the page's own calls.
func checkPageInvocationID(invocationID string) error {
	if invocationID == "" {
		return fmt.Errorf("invocation id is required")
	}
	if extension.IsRuntimeInvocationID(invocationID) {
		return fmt.Errorf("invocation id %q has the shape the extension runtime reserves for its own calls", invocationID)
	}
	return nil
}

// pageCall is one running page tool call.
type pageCall struct {
	extName string
	cancel  func()
}

// begin registers the call of extName named invocationID and returns the context
// it must run under, plus the function that unregisters it once it returns.
// cancelTool is the plugin's Plugin.CancelTool. A second call under an id that
// is still running is refused: cancel could not tell the two apart. A call whose
// cancel already arrived is refused too, and so is an id checkPageInvocationID
// refuses.
func (c *toolCalls) begin(ctx context.Context, extName, invocationID string, cancelTool func(invocationID string) bool) (context.Context, func(), error) {
	if err := checkPageInvocationID(invocationID); err != nil {
		return nil, nil, err
	}
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
		c.running = make(map[string]pageCall)
	}
	ctx, cancelCtx := context.WithCancel(ctx)
	c.running[invocationID] = pageCall{extName: extName, cancel: func() {
		if !cancelTool(invocationID) {
			cancelCtx()
		}
	}}
	end := func() {
		c.mu.Lock()
		delete(c.running, invocationID)
		c.mu.Unlock()
		cancelCtx()
	}
	return ctx, end, nil
}

// cancel ends the call named invocationID and reports whether one was running.
// When none was, the id is held for earlyCancelTTL so the call, should it begin
// late, is refused. An id checkPageInvocationID refuses names no page call and
// is refused here as well.
func (c *toolCalls) cancel(invocationID string) (bool, error) {
	if err := checkPageInvocationID(invocationID); err != nil {
		return false, err
	}
	c.mu.Lock()
	call, ok := c.running[invocationID]
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
		call.cancel()
	}
	return ok, nil
}

// isPageCall reports whether invocationID is a page tool call of extName that is
// still running — the only kind whose events have a listener.
func (c *toolCalls) isPageCall(extName, invocationID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	call, ok := c.running[invocationID]
	return ok && call.extName == extName
}
