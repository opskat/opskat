package system

import (
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
)

func TestGetSSHConnectionSettingsReturnsDefaultsWhenUnset(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	s := New(t.Context(), SkillContent{})
	got := s.GetSSHConnectionSettings()
	want := SSHConnectionSettings{
		KeepAliveIntervalSeconds: int(sshtuning.DefaultKeepAliveInterval.Seconds()),
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestSetSSHConnectionSettingsRoundTripsAndApplies(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	t.Cleanup(func() { sshtuning.Set(sshtuning.Default()) })

	s := New(t.Context(), SkillContent{})
	in := SSHConnectionSettings{KeepAliveIntervalSeconds: 90}
	if err := s.SetSSHConnectionSettings(in); err != nil {
		t.Fatalf("SetSSHConnectionSettings: %v", err)
	}

	// Round-trips through config.
	if got := s.GetSSHConnectionSettings(); got != in {
		t.Fatalf("round-trip = %+v, want %+v", got, in)
	}

	// Applied to the live global tuning so the next connection uses it. The
	// non-configurable knobs stay at their built-in defaults.
	live := sshtuning.Get()
	if !live.TCPNoDelay || !live.TCPKeepAlive {
		t.Fatalf("non-configurable tuning should stay at defaults: %+v", live)
	}
	if live.KeepAliveInterval.Seconds() != 90 {
		t.Fatalf("live keepalive not applied: %+v", live)
	}
	if live.DialTimeout != sshtuning.DefaultDialTimeout {
		t.Fatalf("dial timeout should stay default: %+v", live)
	}
}

func TestSetSSHConnectionSettingsRejectsOutOfRange(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := New(t.Context(), SkillContent{})

	cases := []SSHConnectionSettings{
		{KeepAliveIntervalSeconds: 1},     // interval too small
		{KeepAliveIntervalSeconds: 99999}, // interval too large
		{KeepAliveIntervalSeconds: 0},     // unset / too small
	}
	for i, in := range cases {
		if err := s.SetSSHConnectionSettings(in); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, in)
		}
	}
}
