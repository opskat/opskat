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
	assert.ErrorIs(t, context.Cause(inner), context.DeadlineExceeded)
	<-outer.Done()
	assert.ErrorIs(t, context.Cause(outer), context.DeadlineExceeded)
}
