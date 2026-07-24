package ssh

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForwardManagerCloseAsset(t *testing.T) {
	m := NewForwardManager(nil)
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelA)
	t.Cleanup(cancelB)

	m.running[1] = &runningForward{configID: 10, assetID: 1, cancel: cancelA}
	m.running[2] = &runningForward{configID: 11, assetID: 2, cancel: cancelB}

	m.CloseAsset(1)

	assert.Error(t, ctxA.Err(), "该资产的转发规则应被取消")
	assert.NoError(t, ctxB.Err(), "其它资产的转发规则不应受影响")
	assert.NotContains(t, m.running, int64(1))
	assert.Contains(t, m.running, int64(2))
}
