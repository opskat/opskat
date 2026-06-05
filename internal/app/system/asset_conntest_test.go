package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/testreg"
)

func newTestSystem() *System {
	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()
	return s
}

func TestTestAssetConnectionDispatch(t *testing.T) {
	defer conntest.Unregister("dummy")
	want := errors.New("dial failed")
	var gotCfg, gotPw string
	conntest.Register("dummy", func(_ context.Context, cfg, pw string) error {
		gotCfg, gotPw = cfg, pw
		return want
	})
	err := newTestSystem().TestAssetConnection("tid", "dummy", "CFG", "PW")
	if err != want {
		t.Fatalf("got %v, want %v", err, want)
	}
	if gotCfg != "CFG" || gotPw != "PW" {
		t.Fatalf("tester got cfg=%q pw=%q", gotCfg, gotPw)
	}
}

func TestTestAssetConnectionUnknownType(t *testing.T) {
	if err := newTestSystem().TestAssetConnection("tid", "nope", "{}", ""); err == nil {
		t.Fatal("expected error for unknown asset type")
	}
}

func TestTestAssetConnectionCancellable(t *testing.T) {
	defer conntest.Unregister("blocker")
	started := make(chan struct{})
	conntest.Register("blocker", func(ctx context.Context, _, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- newTestSystem().TestAssetConnection("cancel-me", "blocker", "{}", "") }()
	<-started
	testreg.Cancel("cancel-me")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected non-nil error after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TestAssetConnection did not unblock on cancel")
	}
}
