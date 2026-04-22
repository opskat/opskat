package ai

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCloser struct {
	closed  atomic.Int32
	onClose func()
}

func (f *fakeCloser) Close() error {
	f.closed.Add(1)
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}

// ctx 取消时，所有 closer 应被调用一次。
func TestCloseOnCancel_TriggersOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a, b := &fakeCloser{}, &fakeCloser{}
	done := make(chan struct{})
	a.onClose = func() {
		if b.closed.Load() > 0 {
			close(done)
		}
	}
	b.onClose = func() {
		if a.closed.Load() > 0 {
			close(done)
		}
	}
	stop := closeOnCancel(ctx, a, b)
	defer stop()

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("closers not invoked after cancel: a=%d b=%d", a.closed.Load(), b.closed.Load())
	}
	if a.closed.Load() != 1 || b.closed.Load() != 1 {
		t.Fatalf("expected each closer called once, got a=%d b=%d", a.closed.Load(), b.closed.Load())
	}
}

// 正常路径 stop() 退出 watcher，不应调用任何 closer，避免关闭活连接。
func TestCloseOnCancel_NoCallOnStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &fakeCloser{}
	stop := closeOnCancel(ctx, c)
	stop()
	// 给 watcher 一点时间退出；之后取消 ctx 不应再触发 Close。
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	if c.closed.Load() != 0 {
		t.Fatalf("expected closer not called, got %d", c.closed.Load())
	}
}
