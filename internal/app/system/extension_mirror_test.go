package system

import (
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
)

func TestExtensionMirrorDefaultsToGhcrIo(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := New(t.Context(), SkillContent{})
	if got := s.GetExtensionMirror(); got != "" {
		t.Fatalf("stored mirror = %q, want empty (direct)", got)
	}
	if got := bootstrap.ExtensionRegistryHost(); got != "ghcr.io" {
		t.Fatalf("effective host = %q, want ghcr.io", got)
	}
}

func TestSetExtensionMirrorRoundTrips(t *testing.T) {
	for _, host := range []string{"ghcr.nju.edu.cn", "registry.example.com:5000", "10.0.0.5:5000", "[::1]:5000", "localhost"} {
		initBootstrapForSystemTest(t)
		if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
		s := New(t.Context(), SkillContent{})
		if err := s.SetExtensionMirror(host); err != nil {
			t.Fatalf("SetExtensionMirror(%q): %v", host, err)
		}
		if got := s.GetExtensionMirror(); got != host {
			t.Fatalf("GetExtensionMirror = %q, want %q", got, host)
		}
		if got := bootstrap.ExtensionRegistryHost(); got != host {
			t.Fatalf("effective host = %q, want %q", got, host)
		}
	}
}

func TestSetExtensionMirrorEmptyResetsToDirect(t *testing.T) {
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{ExtensionMirror: "ghcr.nju.edu.cn"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := New(t.Context(), SkillContent{})
	if err := s.SetExtensionMirror(""); err != nil {
		t.Fatalf("SetExtensionMirror: %v", err)
	}
	if got := bootstrap.ExtensionRegistryHost(); got != "ghcr.io" {
		t.Fatalf("effective host = %q, want ghcr.io", got)
	}
}

func TestSetExtensionMirrorRejectsInvalidHostWithoutSaving(t *testing.T) {
	for _, bad := range []string{
		"https://ghcr.nju.edu.cn", "ghcr.nju.edu.cn/v2", "a b.com", " ghcr.io", "ghcr.io ", "host:", "host:0",
		"host:99999", "host:abc", "-bad.com", "bad-.com", "a..b", "ho_st.com", ":5000", "host\t", "[::1", "a@b.com",
	} {
		initBootstrapForSystemTest(t)
		if err := bootstrap.SaveConfig(&bootstrap.AppConfig{ExtensionMirror: "ghcr.nju.edu.cn"}); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
		s := New(t.Context(), SkillContent{})
		if err := s.SetExtensionMirror(bad); err == nil {
			t.Fatalf("SetExtensionMirror(%q) accepted, want error", bad)
		}
		if got := s.GetExtensionMirror(); got != "ghcr.nju.edu.cn" {
			t.Fatalf("after rejecting %q stored = %q, want unchanged", bad, got)
		}
	}
}
