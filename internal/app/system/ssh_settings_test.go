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
		TCPNoDelay:               true,
		TCPKeepAlive:             true,
		KeepAliveIntervalSeconds: int(sshtuning.DefaultKeepAliveInterval.Seconds()),
		ConnectTimeoutSeconds:    int(sshtuning.DefaultDialTimeout.Seconds()),
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
	in := SSHConnectionSettings{
		TCPNoDelay:               false,
		TCPKeepAlive:             false,
		KeepAliveIntervalSeconds: 90,
		ConnectTimeoutSeconds:    15,
	}
	if err := s.SetSSHConnectionSettings(in); err != nil {
		t.Fatalf("SetSSHConnectionSettings: %v", err)
	}

	// Round-trips through config — an explicit false must survive (tri-state),
	// not collapse back to the default true.
	if got := s.GetSSHConnectionSettings(); got != in {
		t.Fatalf("round-trip = %+v, want %+v", got, in)
	}

	// Applied to the live global tuning so the next connection uses it.
	live := sshtuning.Get()
	if live.TCPNoDelay || live.TCPKeepAlive {
		t.Fatalf("live tuning bools not applied: %+v", live)
	}
	if live.KeepAliveInterval.Seconds() != 90 || live.DialTimeout.Seconds() != 15 {
		t.Fatalf("live tuning durations not applied: %+v", live)
	}
}

func TestSetSSHConnectionSettingsRejectsOutOfRange(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := New(t.Context(), SkillContent{})

	cases := []SSHConnectionSettings{
		{KeepAliveIntervalSeconds: 1, ConnectTimeoutSeconds: 30},      // interval too small
		{KeepAliveIntervalSeconds: 99999, ConnectTimeoutSeconds: 30},  // interval too large
		{KeepAliveIntervalSeconds: 60, ConnectTimeoutSeconds: 0},      // timeout too small
		{KeepAliveIntervalSeconds: 60, ConnectTimeoutSeconds: 100000}, // timeout too large
	}
	for i, in := range cases {
		if err := s.SetSSHConnectionSettings(in); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, in)
		}
	}
}
