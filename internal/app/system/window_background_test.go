package system

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/wailsapp/wails/v2/pkg/options"
)

type rgbaCall struct{ r, g, b, a uint8 }

func stubWindowBackground(t *testing.T) *[]rgbaCall {
	t.Helper()
	orig := windowOps
	t.Cleanup(func() { windowOps = orig })
	var calls []rgbaCall
	windowOps.SetBackgroundColour = func(_ context.Context, r, g, b, a uint8) {
		calls = append(calls, rgbaCall{r, g, b, a})
	}
	return &calls
}

func TestSetWindowBackgroundPersistsColorForNextLaunch(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	stubWindowBackground(t)
	s := New(t.Context(), SkillContent{})

	if err := s.SetWindowBackground("#11161E"); err != nil {
		t.Fatalf("SetWindowBackground: %v", err)
	}

	reloaded, err := bootstrap.LoadConfig(bootstrap.AppDataDir())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := WindowBackgroundColor(reloaded)
	want := &options.RGBA{R: 0x11, G: 0x16, B: 0x1e, A: 255}
	if got == nil || *got != *want {
		t.Fatalf("WindowBackgroundColor = %+v, want %+v", got, want)
	}
}

func TestSetWindowBackgroundAppliesToOpenWindow(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	calls := stubWindowBackground(t)
	s := New(t.Context(), SkillContent{})
	s.ctx = context.Background()

	if err := s.SetWindowBackground("#eff2f7"); err != nil {
		t.Fatalf("SetWindowBackground: %v", err)
	}

	want := []rgbaCall{{0xef, 0xf2, 0xf7, 255}}
	if len(*calls) != 1 || (*calls)[0] != want[0] {
		t.Fatalf("SetBackgroundColour calls = %+v, want %+v", *calls, want)
	}
}

func TestSetWindowBackgroundRejectsMalformedColorWithoutSaving(t *testing.T) {
	for _, in := range []string{"", "red", "#fff", "#12345", "#1234567", "11161e", "#gg161e", "rgb(1,2,3)"} {
		initBootstrapForSystemTest(t)
		if err := bootstrap.SaveConfig(&bootstrap.AppConfig{WindowBackground: "#11161e"}); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
		calls := stubWindowBackground(t)
		s := New(t.Context(), SkillContent{})
		s.ctx = context.Background()

		if err := s.SetWindowBackground(in); err == nil {
			t.Fatalf("SetWindowBackground(%q) = nil, want error", in)
		}
		if got := bootstrap.GetConfig().WindowBackground; got != "#11161e" {
			t.Fatalf("after SetWindowBackground(%q) stored = %q, want unchanged #11161e", in, got)
		}
		if len(*calls) != 0 {
			t.Fatalf("SetWindowBackground(%q) applied %+v to the window", in, *calls)
		}
	}
}

func TestWindowBackgroundColorUnsetKeepsWailsDefault(t *testing.T) {
	if got := WindowBackgroundColor(&bootstrap.AppConfig{}); got != nil {
		t.Fatalf("WindowBackgroundColor(empty) = %+v, want nil", got)
	}
	if got := WindowBackgroundColor(nil); got != nil {
		t.Fatalf("WindowBackgroundColor(nil) = %+v, want nil", got)
	}
}
