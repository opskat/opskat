package extension

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrPluginClosed is what a call fails with once its plugin is closed — its
// extension disabled, upgraded or removed. A call cut off by the closing wraps
// the guest's own failure with it.
var ErrPluginClosed = errors.New("plugin closed")

// Instance is a module instance of a plugin leased for a caller's own use
// (Plugin.Lease), outside the plugin's call pool. Unlike a pooled call's, its
// invocation — the IO handle table the guest addresses and the scope its host
// calls are admitted against — lasts as long as the instance, so what the guest
// keeps between calls (a database connection, a cursor) stays reachable.
//
// A WASM instance runs one call at a time: Invoke calls are serialized.
type Instance struct {
	plugin *Plugin
	inst   *instance
	inv    *invocation

	mu sync.Mutex
}

// Lease creates an instance of the plugin bound to scope from its first host
// call: every host call it makes, for as long as it lives, is admitted against
// scope (a scope with Ops restricts the host calls it may make at all) and the
// manifest's permissions. The caller closes it.
func (p *Plugin) Lease(ctx context.Context, scope *Scope) (*Instance, error) {
	if p.closed.Load() {
		return nil, ErrPluginClosed
	}
	inst, err := p.newInstance(ctx)
	if err != nil {
		return nil, err
	}
	return &Instance{plugin: p, inst: inst, inv: newInvocation(p.nextInvocationID(), nil).bindScope(scope)}, nil
}

// ID names the instance's invocation: the id its host calls and events carry.
func (i *Instance) ID() string { return i.inv.id }

// Invoke runs guest function fn with input in on the instance and returns its
// reply. ctx bounds the call: ctx ending while it runs fails whatever host IO the
// guest is blocked in (Interrupt) — the guest never gets back to its own timers
// while blocked — and the call with an error wrapping ctx's. A call cut off by
// the plugin's closing fails with ErrPluginClosed. Any failure leaves the
// instance in an unknown state; the caller should not call it again.
func (i *Instance) Invoke(ctx context.Context, fn string, in []byte) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	req, err := callRequest(fn, in)
	if err != nil {
		return nil, err
	}
	stopInterrupt := context.AfterFunc(ctx, i.inv.close)
	out, err := i.inst.invoke(withInvocation(ctx, i.inv), req)
	interrupted := !stopInterrupt()
	switch {
	case interrupted || (err != nil && ctx.Err() != nil):
		return nil, fmt.Errorf("call %s: %w", fn, ctx.Err())
	case err != nil && i.plugin.closed.Load():
		return nil, fmt.Errorf("%w: %w", ErrPluginClosed, err)
	case err != nil:
		return nil, err
	}
	return out, nil
}

// Interrupt closes every IO handle the guest holds open, failing a host call it
// is blocked in. The instance stays open.
func (i *Instance) Interrupt() { i.inv.close() }

// Close closes the instance's IO handles and the instance itself.
func (i *Instance) Close(ctx context.Context) error {
	i.inv.close()
	return i.inst.mod.Close(ctx)
}

// Invoke runs guest function fn with input in on a pooled instance, with no
// asset and so no scope beyond the manifest's allowlist, bounded by the plugin's
// tool timeout — a request-response call that touches nothing the guest keeps.
func (p *Plugin) Invoke(ctx context.Context, fn string, in []byte) ([]byte, error) {
	return p.call(ctx, newInvocation(p.nextInvocationID(), nil), fn, in, p.opts.toolTimeout)
}
