package helper

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/sshagent"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"
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

// TestRunCommandWithCache_AgentDialErrorNotRetriedAndNoPasswordFallback 覆盖
// "AI 绝不把 Agent 失败转换为密码更新或重试流程"：拨号阶段返回类型化 Agent 错误
// （mfa_required / sign_failed）时，runCommandWithCache 原样返回该错误，不做任何
// 重拨（calls==1），更不切换到密码认证。
func TestRunCommandWithCache_AgentDialErrorNotRetriedAndNoPasswordFallback(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{name: "mfa_required", code: sshagent.CodeMFARequired},
		{name: "sign_failed", code: sshagent.CodeSignFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewSSHClientCache()
			calls := 0
			agentErr := &sshagent.Error{Code: tc.code, Message: "boom"}

			_, err := runCommandWithCacheDial(context.Background(), cache, 1, "uptime", func(_ context.Context, _ int64) (*ssh.Client, io.Closer, error) {
				calls++
				return nil, nil, agentErr
			})

			assert.Equal(t, 1, calls, "agent dial failure must not trigger a retry")
			assert.ErrorIs(t, err, agentErr, "the typed agent error must be returned as-is, not converted")
			code, ok := sshagent.CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, tc.code, code)
		})
	}
}
