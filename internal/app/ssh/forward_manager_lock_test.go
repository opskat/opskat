package ssh

import (
	"context"
	"io"
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
