package sshkeepalive

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

type fakePinger struct {
	mu       sync.Mutex
	count    int32
	called   chan struct{}
	returnFn func() error
	closes   atomic.Int32
}

func (f *fakePinger) Close() error {
	f.closes.Add(1)
	return nil
}

func (f *fakePinger) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	atomic.AddInt32(&f.count, 1)
	if f.called != nil {
		f.called <- struct{}{}
	}
	f.mu.Lock()
	fn := f.returnFn
	f.mu.Unlock()
	if fn != nil {
		return false, nil, fn()
	}
	return true, nil, nil
}

func (f *fakePinger) calls() int32 {
	return atomic.LoadInt32(&f.count)
}

func manualStart(fp *fakePinger) (chan<- time.Time, func()) {
	ticks := make(chan time.Time, 1)
	return ticks, start(fp, ticks, func() {}, time.Hour)
}

func waitForCall(t *testing.T, fp *fakePinger) {
	t.Helper()
	select {
	case <-fp.called:
	case <-time.After(time.Second):
		t.Fatal("keepalive request was not observed")
	}
}

func TestStart(t *testing.T) {
	Convey("Start sends one keepalive per tick", t, func() {
		fp := &fakePinger{called: make(chan struct{}, 2)}
		ticks, stop := manualStart(fp)
		defer stop()

		ticks <- time.Time{}
		waitForCall(t, fp)
		ticks <- time.Time{}
		waitForCall(t, fp)
		stop()

		So(fp.calls(), ShouldEqual, 2)
		So(fp.closes.Load(), ShouldEqual, 0)
	})

	Convey("Start does not fire before the first interval", t, func() {
		fp := &fakePinger{}
		_, stop := manualStart(fp)
		stop()

		So(fp.calls(), ShouldEqual, 0)
	})

	Convey("stop waits for an in-flight ping and no calls happen after it returns", t, func() {
		pingStarted := make(chan struct{})
		releasePing := make(chan struct{})
		var startedOnce sync.Once
		fp := &fakePinger{returnFn: func() error {
			startedOnce.Do(func() { close(pingStarted) })
			<-releasePing
			return nil
		}}
		ticks, stop := manualStart(fp)
		ticks <- time.Time{}
		<-pingStarted

		stopReturned := make(chan struct{})
		go func() {
			stop()
			close(stopReturned)
		}()

		select {
		case <-stopReturned:
			t.Fatal("stop returned while SendRequest was still in flight")
		case <-time.After(100 * time.Millisecond):
		}

		close(releasePing)
		select {
		case <-stopReturned:
		case <-time.After(time.Second):
			t.Fatal("stop did not return after the in-flight SendRequest completed")
		}

		baseline := fp.calls()
		ticks <- time.Time{}
		So(fp.calls(), ShouldEqual, baseline)
	})

	Convey("stop is idempotent", t, func() {
		fp := &fakePinger{}
		_, stop := manualStart(fp)
		stop()
		stop()
		stop()
		So(true, ShouldBeTrue)
	})

	Convey("ping error stops the goroutine", t, func() {
		fp := &fakePinger{
			called:   make(chan struct{}, 1),
			returnFn: func() error { return errors.New("boom") },
		}
		ticks, stop := manualStart(fp)
		ticks <- time.Time{}
		waitForCall(t, fp)
		stop()

		ticks <- time.Time{}
		So(fp.calls(), ShouldEqual, 1)
	})

	Convey("non-positive interval disables the heartbeat", t, func() {
		for _, interval := range []time.Duration{0, -1 * time.Second} {
			fp := &fakePinger{}
			stop := Start(fp, interval)
			stop()
			stop()
			So(fp.calls(), ShouldEqual, 0)
		}
	})
}

// hangingPinger 的 keepalive 永不回复，直到 Close 打断它（与 *ssh.Client 一样：关闭连接后挂起的请求返回错误）。
type hangingPinger struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func (h *hangingPinger) SendRequest(string, bool, []byte) (bool, []byte, error) {
	<-h.closed
	return false, nil, errors.New("connection closed")
}

func (h *hangingPinger) Close() error {
	h.closeOnce.Do(func() { close(h.closed) })
	return nil
}

func TestStartClosesClientWhenReplyTimesOut(t *testing.T) {
	h := &hangingPinger{closed: make(chan struct{})}
	stop := Start(h, 20*time.Millisecond)
	defer stop()
	defer func() { _ = h.Close() }()

	select {
	case <-h.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("client was not closed after the keepalive reply timed out")
	}
}

func TestStartKeepsAnsweredConnectionOpen(t *testing.T) {
	fp := &fakePinger{}
	stop := Start(fp, 50*time.Millisecond)
	// 跑过两个回复时限（3×50ms）：按时回复的连接一次也不能被关。
	time.Sleep(350 * time.Millisecond)
	stop()

	if fp.calls() < 2 {
		t.Fatalf("keepalive calls = %d, want at least 2", fp.calls())
	}
	if n := fp.closes.Load(); n != 0 {
		t.Fatalf("answered connection closed %d times", n)
	}
}
