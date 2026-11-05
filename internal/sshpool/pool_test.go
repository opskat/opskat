package sshpool

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeConn 是 *ssh.Client 底下的 ssh.Conn：用 ssh.NewClient 包一层就得到真的 *ssh.Client，
// 不需要网络。hang 打开时 keepalive 请求永不回复，直到连接被关闭；closeBlock 非空时 Close
// 要等它被关闭才返回（模拟关闭跳板链时卡在网络上）。
type fakeConn struct {
	hang       atomic.Bool
	closeBlock chan struct{}
	closed     chan struct{}
	closeOnce  sync.Once
}

func newFakeConn() *fakeConn { return &fakeConn{closed: make(chan struct{})} }

func (c *fakeConn) User() string          { return "test" }
func (c *fakeConn) SessionID() []byte     { return nil }
func (c *fakeConn) ClientVersion() []byte { return nil }
func (c *fakeConn) ServerVersion() []byte { return nil }
func (c *fakeConn) RemoteAddr() net.Addr  { return &net.TCPAddr{} }
func (c *fakeConn) LocalAddr() net.Addr   { return &net.TCPAddr{} }

func (c *fakeConn) SendRequest(string, bool, []byte) (bool, []byte, error) {
	if c.isClosed() {
		return false, nil, io.EOF
	}
	if c.hang.Load() {
		<-c.closed
		return false, nil, io.EOF
	}
	return true, nil, nil
}

func (c *fakeConn) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	return nil, nil, errors.New("not supported")
}

func (c *fakeConn) Close() error {
	if c.closeBlock != nil {
		<-c.closeBlock
	}
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) Wait() error {
	<-c.closed
	return nil
}

func (c *fakeConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func newFakeClient(c *fakeConn) *ssh.Client {
	chans := make(chan ssh.NewChannel)
	close(chans)
	reqs := make(chan *ssh.Request)
	close(reqs)
	return ssh.NewClient(c, chans, reqs)
}

// fakeDialer 按资产记录拨号次数；next 决定每次拨号返回的连接，gate 非空时拨号要等它关闭。
type fakeDialer struct {
	mu    sync.Mutex
	dials map[int64]int
	conns map[int64][]*fakeConn
	next  func(assetID int64) *fakeConn
	gate  chan struct{}
}

func newFakeDialer() *fakeDialer {
	return &fakeDialer{
		dials: make(map[int64]int),
		conns: make(map[int64][]*fakeConn),
		next:  func(int64) *fakeConn { return newFakeConn() },
	}
}

func (d *fakeDialer) DialAsset(_ context.Context, assetID int64) (*ssh.Client, []io.Closer, error) {
	d.mu.Lock()
	d.dials[assetID]++
	c := d.next(assetID)
	d.conns[assetID] = append(d.conns[assetID], c)
	gate := d.gate
	d.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return newFakeClient(c), nil, nil
}

func (d *fakeDialer) dialCount(assetID int64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials[assetID]
}

func (d *fakeDialer) conn(assetID int64, i int) *fakeConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns[assetID][i]
}

// within 在 d 内等 fn 完成；超时即判定它被阻塞（测试失败而不是挂死）。
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked for more than %v", what, d)
	}
}

const (
	assetHung  int64 = 1
	assetOther int64 = 2
)

// 资产 1 的缓存连接 keepalive 永不回复：它的 Get 在探活超时后重拨，
// 期间资产 2 的 Get、List、空闲清理都不被它卡住。
func TestGetDoesNotBlockOnHungKeepalive(t *testing.T) {
	d := newFakeDialer()
	p := NewPool(d, time.Hour, withProbeTimeout(200*time.Millisecond))
	defer p.Close()
	ctx := context.Background()

	if _, err := p.Get(ctx, assetHung); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	p.Release(assetHung)
	hung := d.conn(assetHung, 0)
	hung.hang.Store(true)
	defer func() { _ = hung.Close() }()

	hungGet := make(chan *ssh.Client, 1)
	go func() {
		c, err := p.Get(ctx, assetHung)
		if err != nil {
			t.Errorf("Get on hung asset: %v", err)
		}
		hungGet <- c
	}()
	// 让那次 Get 进入探活。
	time.Sleep(50 * time.Millisecond)

	within(t, time.Second, "List while another asset's probe hangs", func() { p.List() })
	within(t, time.Second, "cleanupIdle while another asset's probe hangs", p.cleanupIdle)
	within(t, time.Second, "Get of another asset while a probe hangs", func() {
		if _, err := p.Get(ctx, assetOther); err != nil {
			t.Errorf("Get other asset: %v", err)
		}
	})

	select {
	case c := <-hungGet:
		if c == nil {
			t.Fatal("Get on hung asset returned no client")
		}
		if got := d.dialCount(assetHung); got != 2 {
			t.Fatalf("hung asset dials = %d, want 2 (redial after probe timeout)", got)
		}
		if !hung.isClosed() {
			t.Fatal("connection whose probe timed out was not closed")
		}
		if d.conn(assetHung, 1).isClosed() {
			t.Fatal("redialed connection must stay open")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Get on hung asset did not return after the probe timeout")
	}
}

// 空闲清理关闭一条卡在网络上的连接时，不能挡住别的资产的 Get / List。
func TestCleanupIdleClosesOutsideLock(t *testing.T) {
	d := newFakeDialer()
	block := make(chan struct{})
	d.next = func(assetID int64) *fakeConn {
		c := newFakeConn()
		if assetID == assetHung {
			c.closeBlock = block
		}
		return c
	}
	p := NewPool(d, time.Nanosecond)
	defer func() {
		close(block)
		p.Close()
	}()
	ctx := context.Background()

	if _, err := p.Get(ctx, assetHung); err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Release(assetHung)
	time.Sleep(time.Millisecond)

	go p.cleanupIdle()
	// 等清理进入那条连接的 Close。
	time.Sleep(50 * time.Millisecond)

	within(t, time.Second, "List while idle cleanup closes a connection", func() { p.List() })
	within(t, time.Second, "Get while idle cleanup closes a connection", func() {
		if _, err := p.Get(ctx, assetOther); err != nil {
			t.Errorf("Get other asset: %v", err)
		}
	})
	listed := func() bool {
		for _, e := range p.List() {
			if e.AssetID == assetHung {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(time.Second)
	for listed() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if listed() {
		t.Fatal("idle connection still listed after cleanup")
	}
}

// 同一资产 8 个并发首次 Get 只拨号一次，且都拿到同一条连接、各占一个引用。
func TestConcurrentFirstGetDialsOnce(t *testing.T) {
	const n = 8
	d := newFakeDialer()
	gate := make(chan struct{})
	d.gate = gate
	p := NewPool(d, time.Hour)
	defer p.Close()

	clients := make(chan *ssh.Client, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			c, err := p.Get(context.Background(), assetOther)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
			clients <- c
		})
	}
	// 所有 Get 都进入拨号（旧实现）或至多 200ms 后放行拨号。
	deadline := time.Now().Add(200 * time.Millisecond)
	for d.dialCount(assetOther) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(gate)
	within(t, 3*time.Second, "concurrent first Gets", wg.Wait)
	close(clients)

	if got := d.dialCount(assetOther); got != 1 {
		t.Fatalf("dials = %d, want 1", got)
	}
	var first *ssh.Client
	for c := range clients {
		if first == nil {
			first = c
		}
		if c != first {
			t.Fatal("concurrent Gets returned different clients")
		}
	}
	infos := p.List()
	if len(infos) != 1 || infos[0].RefCount != n {
		t.Fatalf("pool entries = %+v, want one entry with ref_count %d", infos, n)
	}
}

// ctxDialer 的第一次拨号一直等到调用方的 ctx 结束并返回它的错误（像真实拨号那样受 ctx 约束），
// 之后的拨号立即成功。
type ctxDialer struct {
	dials   atomic.Int32
	entered chan struct{}
}

func (d *ctxDialer) DialAsset(ctx context.Context, _ int64) (*ssh.Client, []io.Closer, error) {
	if d.dials.Add(1) == 1 {
		d.entered <- struct{}{}
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return newFakeClient(newFakeConn()), nil, nil
}

// 合并拨号的发起方取消了自己的 ctx：它拿到取消错误，但同时在等这次拨号、ctx 仍然有效的
// 调用方不能跟着失败，而是重新拨号拿到连接。
func TestSharedDialCanceledByLeaderDoesNotFailWaiters(t *testing.T) {
	d := &ctxDialer{entered: make(chan struct{}, 1)}
	p := NewPool(d, time.Hour)
	defer p.Close()

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := p.Get(leaderCtx, assetOther)
		leaderErr <- err
	}()
	<-d.entered

	waiterErr := make(chan error, 1)
	go func() {
		c, err := p.Get(context.Background(), assetOther)
		if err == nil && c == nil {
			err = errors.New("nil client")
		}
		waiterErr <- err
	}()
	time.Sleep(50 * time.Millisecond) // 让等待方挂到同一次拨号上
	cancelLeader()

	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader err = %v, want context.Canceled", err)
	}
	within(t, 3*time.Second, "waiter Get", func() {
		if err := <-waiterErr; err != nil {
			t.Errorf("waiter Get failed with the leader's cancellation: %v", err)
		}
	})
}
