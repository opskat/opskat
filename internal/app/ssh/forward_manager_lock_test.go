package ssh

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/model/entity/forward_entity"
	"github.com/opskat/opskat/internal/repository/forward_repo"
)

// fakeForwardRepo serves configs by id; every config has one rule of an
// unsupported type so StartConfig never listens or touches the SSH client.
type fakeForwardRepo struct {
	forward_repo.ForwardRepo
	assets map[int64]int64 // configID → assetID
}

func (f *fakeForwardRepo) FindConfig(_ context.Context, id int64) (*forward_entity.ForwardConfig, error) {
	return &forward_entity.ForwardConfig{ID: id, AssetID: f.assets[id]}, nil
}

func (f *fakeForwardRepo) ListRulesByConfigID(_ context.Context, id int64) ([]*forward_entity.ForwardRule, error) {
	return []*forward_entity.ForwardRule{{ID: id * 100, ConfigID: id, Type: "bogus"}}, nil
}

type blockingDialer struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (d *blockingDialer) DialAsset(context.Context, int64) (*ssh.Client, []io.Closer, error) {
	d.calls.Add(1)
	d.entered <- struct{}{}
	<-d.release
	return &ssh.Client{}, nil, nil
}

func setupForwardRepo(t *testing.T, assets map[int64]int64) {
	t.Helper()
	forward_repo.RegisterForward(&fakeForwardRepo{assets: assets})
}

func TestStartConfig_DialDoesNotBlockOtherConfigs(t *testing.T) {
	setupForwardRepo(t, map[int64]int64{1: 10, 2: 20})
	d := &blockingDialer{entered: make(chan struct{}, 4), release: make(chan struct{})}
	m := NewForwardManager(d)
	t.Cleanup(func() {
		select {
		case <-d.release:
		default:
			close(d.release)
		}
	})
	m.running[200] = &runningForward{configID: 2, assetID: 20, cancel: func() {}}

	started := make(chan error, 1)
	go func() { started <- m.StartConfig(context.Background(), 1) }()
	<-d.entered

	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.Equal(t, "running", m.GetConfigStatus(2))
		assert.Equal(t, "stopped", m.GetConfigStatus(1))
		m.StopConfig(2)
		assert.Equal(t, "stopped", m.GetConfigStatus(2))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Status/Stop blocked while another config is dialing")
	}

	close(d.release)
	assert.NoError(t, <-started)
	assert.Equal(t, "error", m.GetConfigStatus(1))
}

func TestStartConfig_ConcurrentSameAssetDialsOnce(t *testing.T) {
	setupForwardRepo(t, map[int64]int64{1: 10, 2: 10})
	d := &blockingDialer{entered: make(chan struct{}, 4), release: make(chan struct{})}
	m := NewForwardManager(d)

	var wg sync.WaitGroup
	for _, id := range []int64{1, 2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, m.StartConfig(context.Background(), id))
		}()
	}
	<-d.entered
	time.Sleep(100 * time.Millisecond) // let the second caller reach the dial gate
	close(d.release)
	wg.Wait()

	assert.EqualValues(t, 1, d.calls.Load())
	assert.Equal(t, "error", m.GetConfigStatus(1))
	assert.Equal(t, "error", m.GetConfigStatus(2))
}

// closableConn is the ssh.Conn under a real *ssh.Client that records Close.
type closableConn struct {
	closed atomic.Bool
	done   chan struct{}
	once   sync.Once
}

func newClosableConn() *closableConn { return &closableConn{done: make(chan struct{})} }

func (c *closableConn) User() string          { return "test" }
func (c *closableConn) SessionID() []byte     { return nil }
func (c *closableConn) ClientVersion() []byte { return nil }
func (c *closableConn) ServerVersion() []byte { return nil }
func (c *closableConn) RemoteAddr() net.Addr  { return &net.TCPAddr{} }
func (c *closableConn) LocalAddr() net.Addr   { return &net.TCPAddr{} }
func (c *closableConn) SendRequest(string, bool, []byte) (bool, []byte, error) {
	return false, nil, io.EOF
}
func (c *closableConn) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	return nil, nil, io.EOF
}
func (c *closableConn) Close() error {
	c.closed.Store(true)
	c.once.Do(func() { close(c.done) })
	return nil
}
func (c *closableConn) Wait() error { <-c.done; return nil }

// gatedDialer blocks every dial until release is closed and hands out clients whose Close
// is observable. With honorCtx, the first dial instead ends when its ctx does, returning
// the ctx error like a real dial would.
type gatedDialer struct {
	honorCtx bool
	calls    atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	conns    []*closableConn
}

func (d *gatedDialer) DialAsset(ctx context.Context, _ int64) (*ssh.Client, []io.Closer, error) {
	first := d.calls.Add(1) == 1
	d.entered <- struct{}{}
	if first && d.honorCtx {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	<-d.release
	conn := newClosableConn()
	d.mu.Lock()
	d.conns = append(d.conns, conn)
	d.mu.Unlock()
	chans := make(chan ssh.NewChannel)
	close(chans)
	reqs := make(chan *ssh.Request)
	close(reqs)
	return ssh.NewClient(conn, chans, reqs), nil, nil
}

func newGatedDialer() *gatedDialer {
	return &gatedDialer{entered: make(chan struct{}, 4), release: make(chan struct{})}
}

// A Stop issued while the same config is still dialing wins: once the start finishes
// the config is stopped, not left running with rules nobody asked for.
func TestStopConfigDuringDialLeavesConfigStopped(t *testing.T) {
	setupForwardRepo(t, map[int64]int64{1: 10})
	d := newGatedDialer()
	m := NewForwardManager(d)

	started := make(chan error, 1)
	go func() { started <- m.StartConfig(context.Background(), 1) }()
	<-d.entered

	stopped := make(chan struct{})
	go func() {
		m.StopConfig(1)
		close(stopped)
	}()
	time.Sleep(50 * time.Millisecond) // let Stop reach the manager while the dial is in flight
	close(d.release)
	assert.NoError(t, <-started)
	<-stopped

	assert.Equal(t, "stopped", m.GetConfigStatus(1))
}

// Deleting the asset while its connection is still being dialed must not leave that
// connection (or rules on it) behind once the dial returns.
func TestCloseAssetDuringDialDropsTheConnection(t *testing.T) {
	setupForwardRepo(t, map[int64]int64{1: 10})
	d := newGatedDialer()
	m := NewForwardManager(d)

	started := make(chan error, 1)
	go func() { started <- m.StartConfig(context.Background(), 1) }()
	<-d.entered
	m.CloseAsset(10)
	close(d.release)

	assert.Error(t, <-started)
	assert.Equal(t, "stopped", m.GetConfigStatus(1))
	m.mu.Lock()
	assert.Empty(t, m.clients)
	m.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if assert.Len(t, d.conns, 1) {
		assert.True(t, d.conns[0].closed.Load(), "the connection dialed for the deleted asset is closed")
	}
}

// The shared dial runs on the first caller's ctx. When that caller gives up, a config
// still waiting on the same dial with a live ctx dials again instead of failing.
func TestSharedDialCanceledByLeaderDoesNotFailWaiters(t *testing.T) {
	setupForwardRepo(t, map[int64]int64{1: 10, 2: 10})
	d := newGatedDialer()
	d.honorCtx = true
	close(d.release)
	m := NewForwardManager(d)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leader := make(chan error, 1)
	go func() { leader <- m.StartConfig(leaderCtx, 1) }()
	<-d.entered

	waiter := make(chan error, 1)
	go func() { waiter <- m.StartConfig(context.Background(), 2) }()
	time.Sleep(50 * time.Millisecond) // let config 2 wait on config 1's dial
	cancelLeader()

	assert.ErrorIs(t, <-leader, context.Canceled)
	select {
	case err := <-waiter:
		assert.NoError(t, err, "config 2 failed with config 1's cancellation")
	case <-time.After(3 * time.Second):
		t.Fatal("config 2 never finished starting")
	}
	assert.Equal(t, "error", m.GetConfigStatus(2))
}
