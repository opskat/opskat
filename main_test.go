package main

import (
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
)

func TestInitialWindowSizeUsesSavedSizeWithMinimumFallbacks(t *testing.T) {
	t.Parallel()

	width, height := initialWindowSize(&bootstrap.AppConfig{
		WindowWidth:  minWindowWidth + 120,
		WindowHeight: minWindowHeight + 80,
	})
	if width != minWindowWidth+120 {
		t.Fatalf("width = %d, want %d", width, minWindowWidth+120)
	}
	if height != minWindowHeight+80 {
		t.Fatalf("height = %d, want %d", height, minWindowHeight+80)
	}

	width, height = initialWindowSize(&bootstrap.AppConfig{
		WindowWidth:  minWindowWidth - 1,
		WindowHeight: minWindowHeight - 1,
	})
	if width != defaultWindowWidth {
		t.Fatalf("width below minimum = %d, want default %d", width, defaultWindowWidth)
	}
	if height != defaultWindowHeight {
		t.Fatalf("height below minimum = %d, want default %d", height, defaultWindowHeight)
	}
}
