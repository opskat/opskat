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
	invalidators = map[string]Invalidator{}
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

// TestInvalidateAsset_OnlyInvalidators 钉住两类钩子的分工：配置改了要丢掉缓存/池化的
// 连接（下次用时按新配置重连），但**不能**掐掉用户正在用的交互式会话——改个备注就把
// 人家开着的终端踢了，是比连着旧配置更糟的行为。
func TestInvalidateAsset_OnlyInvalidators(t *testing.T) {
	reset(t)
	sessionClosed, cacheDropped := false, false
	Register("session", func(_ context.Context, _ int64) error { sessionClosed = true; return nil })
	RegisterInvalidator("pool", func(_ context.Context, _ int64) error { cacheDropped = true; return nil })

	InvalidateAsset(context.Background(), 7)

	assert.True(t, cacheDropped, "缓存/池必须被丢弃")
	assert.False(t, sessionClosed, "交互式会话不该因为改配置被关掉")
}

// TestCloseAsset_AlsoRunsInvalidators 钉住 CloseAsset 是 InvalidateAsset 的超集：
// 资产删了，缓存的连接当然也不能留。让删除路径自动覆盖失效路径，注册方就不必为了
// "删除时也要丢缓存"再登记一遍，也就没有漏登记的可能。
func TestCloseAsset_AlsoRunsInvalidators(t *testing.T) {
	reset(t)
	sessionClosed, cacheDropped := false, false
	Register("session", func(_ context.Context, _ int64) error { sessionClosed = true; return nil })
	RegisterInvalidator("pool", func(_ context.Context, _ int64) error { cacheDropped = true; return nil })

	CloseAsset(context.Background(), 7)

	assert.True(t, sessionClosed)
	assert.True(t, cacheDropped)
}

func TestInvalidateAsset_PanickingInvalidatorIsIsolated(t *testing.T) {
	reset(t)
	called := false
	RegisterInvalidator("boom", func(_ context.Context, _ int64) error { panic("boom") })
	RegisterInvalidator("ok", func(_ context.Context, _ int64) error { called = true; return nil })

	assert.NotPanics(t, func() { InvalidateAsset(context.Background(), 1) })
	assert.True(t, called, "一个实现 panic 不该拖累其它实现")
}

func TestRegisterInvalidator_RejectsInvalidArgs(t *testing.T) {
	reset(t)
	assert.Panics(t, func() { RegisterInvalidator("", func(context.Context, int64) error { return nil }) })
	assert.Panics(t, func() { RegisterInvalidator("x", nil) })
}

// TestCloseAsset_SameNameInBothTablesBothRun 钉住两张表不合并：同一个协议在
// Register 和 RegisterInvalidator 里用同一个 name 是正常写法（ssh 的会话关闭与
// 隧道池失效都叫 "ssh"），按 name 合并会让其中一个被静默吃掉。
func TestCloseAsset_SameNameInBothTablesBothRun(t *testing.T) {
	reset(t)
	var ran []string
	Register("ssh", func(_ context.Context, _ int64) error { ran = append(ran, "session"); return nil })
	RegisterInvalidator("ssh", func(_ context.Context, _ int64) error { ran = append(ran, "pool"); return nil })

	CloseAsset(context.Background(), 1)

	assert.ElementsMatch(t, []string{"session", "pool"}, ran)
}
