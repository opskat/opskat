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
	return ticks, start(fp, ticks, func() {})
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

		So(fp.calls(), ShouldEqual, 2)
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
