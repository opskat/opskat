package main

import (
	"regexp"
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	. "github.com/smartystreets/goconvey/convey"
)

// TestSingleInstanceID 锁定便携版与已安装版不抢同一把单实例锁：便携版若沿用
// 常量 id，会在 bootstrap.Init 写完便携目录后被 Wails 静默 os.Exit(0)。
func TestSingleInstanceID(t *testing.T) {
	t.Parallel()

	t.Run("非便携用历史常量 id", func(t *testing.T) {
		if got := singleInstanceID(""); got != singleInstanceBaseID {
			t.Errorf("singleInstanceID(\"\") = %q, 期望 %q", got, singleInstanceBaseID)
		}
	})

	t.Run("便携版与已安装版 id 不同", func(t *testing.T) {
		if got := singleInstanceID("/Volumes/USB/opskat/data"); got == singleInstanceBaseID {
			t.Errorf("便携 id 与已安装 id 相同 (%q)：会被静默 exit(0)", got)
		}
	})

	t.Run("不同便携目录 id 不同", func(t *testing.T) {
		a := singleInstanceID("/Volumes/USB/opskat/data")
		b := singleInstanceID("/Volumes/USB/opskat-2/data")
		if a == b {
			t.Errorf("两个便携目录 id 相同 (%q)", a)
		}
	})

	t.Run("同一目录 id 稳定", func(t *testing.T) {
		if a, b := singleInstanceID("/opt/opskat/data"), singleInstanceID("/opt/opskat/data"); a != b {
			t.Errorf("id 不稳定: %q != %q", a, b)
		}
	})

	// id 会被 Wails 用作 Windows 命名 mutex，并在 Linux 侧派生 dbus 名，
	// 必须只含安全字符且不能过长。
	t.Run("id 是文件名/mutex 安全的短字符串", func(t *testing.T) {
		got := singleInstanceID("/Volumes/USB/opskat/data")
		if !regexp.MustCompile(`^com\.opskat\.desktop\.[0-9a-f]{8}$`).MatchString(got) {
			t.Errorf("singleInstanceID = %q, 期望 com.opskat.desktop.<8位十六进制>", got)
		}
	})
}

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
