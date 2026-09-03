package extension

import (
	"context"
	"fmt"
)

// invocation is the state that belongs to one WASM call and dies with it: the
// IO handle table the guest may address, and the cancellation flag a long
// running action polls.
//
// Both used to hang off the HostProvider, which was only safe because Plugin.mu
// serialized every call into a plugin. With a reactor instance pool several
// calls run concurrently, so a plugin-wide handle table would let one call read
// another's file descriptor, and a plugin-wide cancellation would stop every
// action at once.
type invocation struct {
	io     *IOHandleManager
	cancel *ActionCancellation // nil for calls that are not actions
}

func newInvocation(cancel *ActionCancellation) *invocation {
	return &invocation{io: NewIOHandleManager(), cancel: cancel}
}

// close releases everything the guest left open.
func (inv *invocation) close() {
	inv.io.CloseAll()
}

func (inv *invocation) shouldStop() bool {
	return inv.cancel != nil && inv.cancel.ShouldStop()
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
