package proxychain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An extension's TCP dial timeout wraps the hop's handshake timeout: a wait for an
// answer inside the handshake stops both, and afterwards each keeps bounding the
// network with what was left of it — not a fresh budget.
func TestAwaitAnswerStopsEveryDialTimeoutOnTheChain(t *testing.T) {
	outer, cancelOuter := WithDialTimeout(context.Background(), 150*time.Millisecond)
	defer cancelOuter()
	inner, cancelInner := WithDialTimeout(outer, 150*time.Millisecond)
	defer cancelInner()

	time.Sleep(50 * time.Millisecond)
	resume := AwaitAnswer(inner)
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, outer.Err(), "the wait for an answer must not count against the outer timeout")
	require.NoError(t, inner.Err(), "the wait for an answer must not count against the inner timeout")
	resume()

	select {
	case <-inner.Done():
	case <-time.After(time.Second):
		t.Fatal("the timeout must keep bounding the network after the answer")
	}
	assert.ErrorIs(t, inner.Err(), context.DeadlineExceeded)
	<-outer.Done()
	assert.ErrorIs(t, outer.Err(), context.DeadlineExceeded)
}

// A dial reads ctx.Err() — usually through a ctx it derived (net.Dialer, a TLS or SSH
// handshake) — to tell a timeout from a canceled call, so an expired dial timeout
// ends its ctx and every ctx derived from it with DeadlineExceeded, while canceling
// the caller still reads as Canceled.
func TestDialTimeoutEndsWithDeadlineExceeded(t *testing.T) {
	ctx, cancel := WithDialTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	derived, cancelDerived := context.WithCancel(ctx)
	defer cancelDerived()

	select {
	case <-derived.Done():
	case <-time.After(time.Second):
		t.Fatal("the dial timeout must end the ctx")
	}
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	assert.ErrorIs(t, derived.Err(), context.DeadlineExceeded)

	parent, cancelParent := context.WithCancel(context.Background())
	canceled, cancelCanceled := WithDialTimeout(parent, time.Minute)
	defer cancelCanceled()
	cancelParent()
	select {
	case <-canceled.Done():
	case <-time.After(time.Second):
		t.Fatal("canceling the caller must end the ctx")
	}
	assert.ErrorIs(t, canceled.Err(), context.Canceled)
}
