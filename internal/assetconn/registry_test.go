package assetconn

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// reset 清空注册表，让每个用例从干净状态开始。
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	closers = map[string]Closer{}
	mu.Unlock()
}

func TestCloseAsset_DispatchesToAllClosers(t *testing.T) {
	reset(t)

	var gotA, gotB int64
	Register("a", func(_ context.Context, assetID int64) error { gotA = assetID; return nil })
	Register("b", func(_ context.Context, assetID int64) error { gotB = assetID; return nil })

	CloseAsset(context.Background(), 42)

	assert.Equal(t, int64(42), gotA)
	assert.Equal(t, int64(42), gotB)
}

func TestCloseAsset_NoCloserRegistered(t *testing.T) {
	reset(t)
	// 没有任何注册项时不应 panic
	CloseAsset(context.Background(), 1)
}

func TestCloseAsset_PanickingCloserIsIsolated(t *testing.T) {
	reset(t)

	called := false
	Register("boom", func(_ context.Context, _ int64) error { panic("boom") })
	Register("ok", func(_ context.Context, _ int64) error { called = true; return nil })

	CloseAsset(context.Background(), 7)

	assert.True(t, called, "一个 closer panic 不应中断其它 closer")
}

func TestCloseAsset_FailingCloserDoesNotStopOthers(t *testing.T) {
	reset(t)

	called := false
	Register("fail", func(_ context.Context, _ int64) error { return errors.New("close failed") })
	Register("ok", func(_ context.Context, _ int64) error { called = true; return nil })

	CloseAsset(context.Background(), 7)

	assert.True(t, called)
}

func TestRegister_RejectsInvalidArgs(t *testing.T) {
	reset(t)

	assert.Panics(t, func() { Register("", func(context.Context, int64) error { return nil }) })
	assert.Panics(t, func() { Register("nil", nil) })
}

func TestRegister_SameNameKeepsOnlyLatest(t *testing.T) {
	reset(t)

	var calls []string
	Register("mgr", func(context.Context, int64) error { calls = append(calls, "old"); return nil })
	Register("mgr", func(context.Context, int64) error { calls = append(calls, "new"); return nil })

	CloseAsset(context.Background(), 1)

	assert.Equal(t, []string{"new"}, calls)
}
