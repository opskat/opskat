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
	if got := bootstrap.ExtensionRegistry(); got != "ghcr.io" {
		t.Fatalf("effective registry = %q, want ghcr.io", got)
	}
}

func TestSetExtensionMirrorRoundTrips(t *testing.T) {
	for _, host := range []string{
		"ghcr.nju.edu.cn", "registry.example.com:5000", "10.0.0.5:5000", "[::1]:5000", "localhost",
		// 镜像站把上游 registry 放在路径下：主机后可以跟仓库路径前缀。
		"mirror.example.com/ghcr.io", "harbor.example.com:8443/proxy-cache/gh", "[::1]:5000/ghcr.io", "10.0.0.5/a_b/c__d",
	} {
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
		if got := bootstrap.ExtensionRegistry(); got != host {
			t.Fatalf("effective registry = %q, want %q", got, host)
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
	if got := bootstrap.ExtensionRegistry(); got != "ghcr.io" {
		t.Fatalf("effective registry = %q, want ghcr.io", got)
	}
}

func TestSetExtensionMirrorRejectsInvalidHostWithoutSaving(t *testing.T) {
	for _, bad := range []string{
		"https://ghcr.nju.edu.cn", "ghcr.nju.edu.cn/v2", "a b.com", " ghcr.io", "ghcr.io ", "host:", "host:0",
		"host:99999", "host:abc", "-bad.com", "bad-.com", "a..b", "ho_st.com", ":5000", "host\t", "[::1", "a@b.com",
		// 路径前缀只能是仓库路径：/v2 是 registry 的接口根，不是仓库。
		"mirror.example.com/", "mirror.example.com/ghcr.io/", "mirror.example.com//ghcr.io", "/ghcr.io",
		"mirror.example.com/v2/ghcr.io", "mirror.example.com/GHCR.io", "mirror.example.com/a b", "mirror.example.com/../x",
		"mirror.example.com/ghcr.io?x=1", "mirror.example.com/ghcr.io#x", "mirror.example.com/ghcr.io:tag", "mirror.example.com/-a",
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
