package extension

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// invocation is the state that belongs to one WASM call and dies with it: the
// IO handle table the guest may address, the cancellation a tool call polls
// through ShouldStop, and the scope its host calls are admitted against.
//
// The reactor instance pool runs several calls of one plugin concurrently, so a
// plugin-wide handle table would let one call read another's file descriptor,
// and a plugin-wide cancellation would stop every call at once.
type invocation struct {
	// id names this run for the whole system: the caller cancels by it, and every
	// event the guest emits is stamped with it so a listener can tell two
	// concurrent runs of the same extension apart.
	id     string
	io     *IOHandleManager
	cancel *toolCancellation // nil for calls that are not tool calls
	// asset is the asset the host scoped this call to (nil when unscoped). It is
	// the only asset whose config the guest may read: the id is the host's, never
	// the guest's.
	asset *AssetRef

	// scope is what the call may reach (see Scope): bound when the call starts
	// (bindScope) or resolved on its first network or ticket host call
	// (scopeOf), and kept for the rest of the call — a resolution error too.
	scopeMu  sync.Mutex
	scope    *Scope
	scopeErr error
	resolved bool
	// connectErr is the failure of the scope's Connect, kept for the call.
	connectErr error
}

func newInvocation(id string, cancel *toolCancellation) *invocation {
	return &invocation{id: id, io: NewIOHandleManager(), cancel: cancel}
}

// scopedTo records the asset the host scoped this call to. Its scope is
// resolved when the call first needs it.
func (inv *invocation) scopedTo(asset *AssetRef) *invocation {
	inv.asset = asset
	return inv
}

// bindScope binds scope to the call from its start — a database driver
// session's, complete with its Ops — so every host call, the first included, is
// admitted against it.
func (inv *invocation) bindScope(scope *Scope) *invocation {
	inv.asset = scope.Asset
	inv.scope = scope
	inv.resolved = true
	return inv
}

// boundScope is the scope a host call's kind is checked against: only a scope
// bound when the call started restricts ops, so nothing is resolved here.
func (inv *invocation) boundScope() *Scope {
	inv.scopeMu.Lock()
	defer inv.scopeMu.Unlock()
	return inv.scope
}

// scopeOf returns the call's scope, resolving it with resolve the first time it
// is needed. A call with no asset has an empty scope: nothing to reach but the
// allowlist. A resolver's scope may not restrict ops (Scope.Ops).
func (inv *invocation) scopeOf(resolve func(*AssetRef) (*Scope, error)) (*Scope, error) {
	inv.scopeMu.Lock()
	defer inv.scopeMu.Unlock()
	if inv.resolved {
		return inv.scope, inv.scopeErr
	}
	inv.resolved = true
	if inv.asset == nil {
		inv.scope = &Scope{}
		return inv.scope, nil
	}
	scope, err := resolve(inv.asset)
	switch {
	case err != nil:
		inv.scopeErr = err
	case scope.Ops != nil:
		inv.scopeErr = fmt.Errorf("scope of asset %s: a resolved scope may not restrict host calls", assetLabel(inv.asset))
	default:
		// The call is the host's to scope: whatever the resolver echoed, its
		// asset is the one the call was scoped to (an ad-hoc config included).
		scope.Asset = inv.asset
		inv.scope = scope
	}
	return inv.scope, inv.scopeErr
}

// connectScope completes scope (the call's, from scopeOf) with its connection
// path the first time a stream needs it (Scope.Connect); a failure is kept and
// returned to every later stream that needs the path.
func (inv *invocation) connectScope(ctx context.Context, scope *Scope) error {
	inv.scopeMu.Lock()
	defer inv.scopeMu.Unlock()
	if inv.connectErr != nil || scope.Connect == nil {
		return inv.connectErr
	}
	conn, err := scope.Connect(ctx)
	if err != nil {
		inv.connectErr = err
		return err
	}
	scope.Route, scope.TLS, scope.Fingerprint = conn.Route, conn.TLS, conn.Fingerprint
	scope.Connect = nil
	return nil
}

// close releases everything the guest left open.
func (inv *invocation) close() {
	inv.io.CloseAll()
}

// stop cancels a tool call: it raises the flag the guest polls, starts the grace
// period after which the call is cut off, and fails whatever host IO the guest
// is blocked in, since a guest waiting on a read that never completes would
// never get to poll the flag — a stream still being opened included (see
// IOHandleManager.RegisterSince). Handles opened afterwards still work, so a
// guest that notices the stop can clean up (abort an upload) before it returns.
func (inv *invocation) stop(grace time.Duration) {
	inv.cancel.cancel(grace)
	inv.io.CloseAll()
}

func (inv *invocation) shouldStop() bool {
	return inv.cancel != nil && inv.cancel.shouldStop()
}

type invocationKeyType struct{}

// invocationKey addresses the invocation inside the context wazero hands to
// host functions — the context passed to api.Function.Call is the one host
// functions invoked from that call receive.
var invocationKey invocationKeyType

func withInvocation(ctx context.Context, inv *invocation) context.Context {
	return context.WithValue(ctx, invocationKey, inv)
}

// invocationFrom returns the invocation a host call belongs to. A missing
// invocation means a host function ran outside any call — the WASM boundary is
// the one place this must be reported rather than assumed away.
func invocationFrom(ctx context.Context) (*invocation, error) {
	inv, ok := ctx.Value(invocationKey).(*invocation)
	if !ok {
		return nil, fmt.Errorf("host call outside of an extension invocation")
	}
	return inv, nil
}

// assetLabel names an asset in an error or log line: by its name when it has
// one, else by its id.
func assetLabel(a *AssetRef) string {
	if a.Name != "" {
		return fmt.Sprintf("%q", a.Name)
	}
	return fmt.Sprintf("#%d", a.ID)
}
