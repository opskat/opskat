package main

import (
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	. "github.com/smartystreets/goconvey/convey"
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

func TestResolveBootstrap(t *testing.T) {
	Convey("with e2e env overrides set", t, func() {
		t.Setenv("OPSKAT_DATA_DIR", "/tmp/opskat-e2e-xyz")
		t.Setenv("OPSKAT_MASTER_KEY", "test-master-key")
		t.Setenv("OPSKAT_E2E", "1")

		dataDir, opts, disableSingleInstance := resolveBootstrap()

		So(dataDir, ShouldEqual, "/tmp/opskat-e2e-xyz")
		So(opts.DataDir, ShouldEqual, "/tmp/opskat-e2e-xyz")
		So(opts.MasterKey, ShouldEqual, "test-master-key")
		So(disableSingleInstance, ShouldBeTrue)
	})

	Convey("with no env overrides", t, func() {
		t.Setenv("OPSKAT_DATA_DIR", "")
		t.Setenv("OPSKAT_MASTER_KEY", "")
		t.Setenv("OPSKAT_E2E", "")

		dataDir, opts, disableSingleInstance := resolveBootstrap()

		So(dataDir, ShouldEqual, bootstrap.AppDataDir())
		So(opts.DataDir, ShouldEqual, bootstrap.AppDataDir())
		So(opts.MasterKey, ShouldEqual, "")
		So(disableSingleInstance, ShouldBeFalse)
	})
}
