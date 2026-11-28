package system

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func stubWindowShow(t *testing.T) *atomic.Int32 {
	t.Helper()
	orig := windowOps
	t.Cleanup(func() { windowOps = orig })
	var shows atomic.Int32
	windowOps.Show = func(context.Context) { shows.Add(1) }
	return &shows
}

func waitForShows(t *testing.T, shows *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for shows.Load() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := shows.Load(); got != want {
		t.Fatalf("window shown %d times, want %d", got, want)
	}
}

func TestBootScreenShownShowsHiddenWindowOnce(t *testing.T) {
	shows := stubWindowShow(t)
	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()

	s.BootScreenShown()
	s.BootScreenShown()

	if got := shows.Load(); got != 1 {
		t.Fatalf("window shown %d times, want 1", got)
	}
}

func TestBootShowTimeoutShowsWindowWhenLoadingScreenNeverReports(t *testing.T) {
	shows := stubWindowShow(t)
	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()

	s.startBootShowTimeout(10 * time.Millisecond)

	waitForShows(t, shows, 1)
}

func TestBootShowTimeoutAfterReportDoesNotShowAgain(t *testing.T) {
	shows := stubWindowShow(t)
	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()

	s.startBootShowTimeout(10 * time.Millisecond)
	s.BootScreenShown()
	time.Sleep(50 * time.Millisecond)

	if got := shows.Load(); got != 1 {
		t.Fatalf("window shown %d times, want 1", got)
	}
}
