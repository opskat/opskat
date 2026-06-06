package system

import (
	"context"
	"testing"
)

func TestActivateWindowOnlyUnminimisesMinimisedWindow(t *testing.T) {
	orig := windowOps
	t.Cleanup(func() { windowOps = orig })

	var calls []string
	windowOps = windowRuntimeOps{
		IsMinimised: func(context.Context) bool {
			calls = append(calls, "isMinimised")
			return false
		},
		Unminimise: func(context.Context) {
			calls = append(calls, "unminimise")
		},
		Show: func(context.Context) {
			calls = append(calls, "show")
		},
		SetAlwaysOnTop: func(_ context.Context, onTop bool) {
			if onTop {
				calls = append(calls, "top")
				return
			}
			calls = append(calls, "notTop")
		},
	}

	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()
	s.ActivateWindow()

	want := []string{"isMinimised", "show", "top", "notTop"}
	if !equalStringSlices(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestActivateWindowRestoresMinimisedWindowBeforeShowing(t *testing.T) {
	orig := windowOps
	t.Cleanup(func() { windowOps = orig })

	var calls []string
	windowOps = windowRuntimeOps{
		IsMinimised: func(context.Context) bool {
			calls = append(calls, "isMinimised")
			return true
		},
		Unminimise: func(context.Context) {
			calls = append(calls, "unminimise")
		},
		Show: func(context.Context) {
			calls = append(calls, "show")
		},
		SetAlwaysOnTop: func(_ context.Context, onTop bool) {
			if onTop {
				calls = append(calls, "top")
				return
			}
			calls = append(calls, "notTop")
		},
	}

	s := New(context.Background(), SkillContent{})
	s.ctx = context.Background()
	s.ActivateWindow()

	want := []string{"isMinimised", "unminimise", "show", "top", "notTop"}
	if !equalStringSlices(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
