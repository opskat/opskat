package system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

var initBootstrapForSystemTestOnce sync.Once

func initBootstrapForSystemTest(t *testing.T) {
	t.Helper()
	initBootstrapForSystemTestOnce.Do(func() {
		dataDir, err := os.MkdirTemp("", "opskat-system-test-*")
		if err != nil {
			t.Fatalf("MkdirTemp: %v", err)
		}
		if _, err := bootstrap.LoadConfig(dataDir); err != nil {
			t.Fatalf("bootstrap.LoadConfig: %v", err)
		}
		credential_svc.SetDefault(credential_svc.New("system-test", []byte("1234567890abcdef")))
	})
}

func TestPathTraversesSymlink(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	linkDir := filepath.Join(root, "link")
	if err = os.Symlink(realDir, linkDir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlinks on Windows requires developer mode or elevated privileges: %v", err)
		}
		t.Fatalf("Symlink: %v", err)
	}

	t.Run("real path returns false", func(t *testing.T) {
		t.Parallel()
		if pathTraversesSymlink(filepath.Join(realDir, "sub", "nested")) {
			t.Fatalf("real path should not report symlink")
		}
	})

	t.Run("path under symlink returns true", func(t *testing.T) {
		t.Parallel()
		if !pathTraversesSymlink(filepath.Join(linkDir, "sub", "nested")) {
			t.Fatalf("path under symlinked dir should report symlink")
		}
	})

	t.Run("symlink itself returns true", func(t *testing.T) {
		t.Parallel()
		if !pathTraversesSymlink(linkDir) {
			t.Fatalf("symlink target itself should report symlink")
		}
	})

	t.Run("nonexistent path under real dir returns false", func(t *testing.T) {
		t.Parallel()
		if pathTraversesSymlink(filepath.Join(realDir, "does", "not", "exist")) {
			t.Fatalf("nonexistent path under real dir should not report symlink")
		}
	})
}

func TestUninstallSkillRemovesSingleSkillTarget(t *testing.T) {
	home := setTestHome(t)
	s := New(t.Context(), SkillContent{})

	targets := map[string]string{
		"codex":      filepath.Join(home, ".codex", "skills", "opsctl"),
		"opencode":   filepath.Join(home, ".config", "opencode", "skills", "opsctl"),
		"gemini-cli": filepath.Join(home, ".gemini", "extensions", "opsctl"),
	}
	for key, path := range targets {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", key, err)
		}
		if err := os.WriteFile(filepath.Join(path, "marker.txt"), []byte(key), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", key, err)
		}
	}

	if err := s.UninstallSkill("codex"); err != nil {
		t.Fatalf("UninstallSkill codex: %v", err)
	}
	if _, err := os.Stat(targets["codex"]); !os.IsNotExist(err) {
		t.Fatalf("codex target should be removed, stat err=%v", err)
	}
	for _, key := range []string{"opencode", "gemini-cli"} {
		if _, err := os.Stat(targets[key]); err != nil {
			t.Fatalf("%s target should remain: %v", key, err)
		}
	}
}

func TestUninstallSkillHandlesUnknownAndMissingTargets(t *testing.T) {
	setTestHome(t)
	s := New(t.Context(), SkillContent{})

	if err := s.UninstallSkill("does-not-exist"); err == nil {
		t.Fatalf("unknown key should return error")
	}
	if err := s.UninstallSkill("codex"); err != nil {
		t.Fatalf("missing target should be treated as success: %v", err)
	}
}

func TestWebDAVExportDefaultsRoundTripEncryptedPassword(t *testing.T) {
	initBootstrapForSystemTest(t)
	cfg := &bootstrap.AppConfig{
		WebDAVURL:      "https://example.com/dav/",
		WebDAVAuthType: string(backup_svc.WebDAVAuthNone),
	}
	if err := bootstrap.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	s := New(t.Context(), SkillContent{})
	// include 标志按原值持久化，与是否真的有 payload 可序列化无关。让标志与 payload 在
	// 两个方向上都不一致，以此锁定该独立性：快捷键标志为开但无 payload；主题标志为关但带 payload。
	opts := backup_svc.ExportOptions{
		IncludeCredentials:  true,
		IncludeForwards:     false,
		IncludePolicyGroups: true,
		IncludeShortcuts:    true,
		IncludeThemes:       false,
		CustomThemes:        `[{"name":"solarized"}]`,
	}

	if err := s.saveWebDAVExportDefaults("backup-password", opts); err != nil {
		t.Fatalf("saveWebDAVExportDefaults: %v", err)
	}
	if cfg.WebDAVExportPassword == "backup-password" {
		t.Fatalf("backup password should be encrypted at rest")
	}

	stored, err := s.GetWebDAVConfig()
	if err != nil {
		t.Fatalf("GetWebDAVConfig: %v", err)
	}
	if !stored.ExportDefaultsConfigured {
		t.Fatalf("export defaults should be marked configured")
	}
	if stored.ExportPassword != "backup-password" {
		t.Fatalf("export password = %q", stored.ExportPassword)
	}
	if !stored.ExportIncludeCredentials {
		t.Fatalf("include credentials should round-trip true")
	}
	if stored.ExportIncludeForwards {
		t.Fatalf("include forwards should round-trip false")
	}
	if !stored.ExportIncludePolicyGroups {
		t.Fatalf("include policy groups should round-trip true")
	}
	if !stored.ExportIncludeShortcuts {
		t.Fatalf("include shortcuts flag should round-trip true even with an empty shortcuts payload")
	}
	if stored.ExportIncludeThemes {
		t.Fatalf("include themes flag should round-trip false even when a custom themes payload is present")
	}
}

func TestClearWebDAVConfigClearsExportDefaults(t *testing.T) {
	initBootstrapForSystemTest(t)
	cfg := &bootstrap.AppConfig{
		WebDAVURL:                       "https://example.com/dav/",
		WebDAVAuthType:                  string(backup_svc.WebDAVAuthNone),
		WebDAVExportDefaultsConfigured:  true,
		WebDAVExportPassword:            "encrypted",
		WebDAVExportIncludeCredentials:  true,
		WebDAVExportIncludeForwards:     true,
		WebDAVExportIncludePolicyGroups: true,
		WebDAVExportIncludeShortcuts:    true,
		WebDAVExportIncludeThemes:       true,
	}
	if err := bootstrap.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	s := New(t.Context(), SkillContent{})
	if err := s.ClearWebDAVConfig(); err != nil {
		t.Fatalf("ClearWebDAVConfig: %v", err)
	}
	if cfg.WebDAVExportDefaultsConfigured || cfg.WebDAVExportPassword != "" || cfg.WebDAVExportIncludeCredentials || cfg.WebDAVExportIncludeForwards || cfg.WebDAVExportIncludePolicyGroups || cfg.WebDAVExportIncludeShortcuts || cfg.WebDAVExportIncludeThemes {
		t.Fatalf("WebDAV export defaults should be cleared: %#v", cfg)
	}
}

func TestUninstallSkillRemovesClaudeRegistration(t *testing.T) {
	home := setTestHome(t)
	s := New(t.Context(), SkillContent{})
	pluginDir := claudePluginDir(home)
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("MkdirAll pluginDir: %v", err)
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll pluginsDir: %v", err)
	}

	writeJSONFile(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"opsctl@opskat": []any{
				map[string]any{"scope": "user", "installPath": pluginDir, "version": pluginVersion},
				map[string]any{"scope": "project", "installPath": "keep", "version": "9.9.9"},
			},
			"other@market": []any{map[string]any{"scope": "user"}},
		},
	})
	writeJSONFile(t, filepath.Join(pluginsDir, "known_marketplaces.json"), map[string]any{
		"opskat": map[string]any{"installLocation": claudeMarketplaceDir(home)},
		"other":  map[string]any{"installLocation": "keep"},
	})
	settingsFile := filepath.Join(home, ".claude", "settings.json")
	writeJSONFile(t, settingsFile, map[string]any{
		"theme": "dark",
		"enabledPlugins": map[string]any{
			"opsctl@opskat": true,
			"other@market":  true,
		},
		"extraKnownMarketplaces": map[string]any{
			"opskat": map[string]any{"source": map[string]any{"source": "directory"}},
			"other":  map[string]any{"source": map[string]any{"source": "directory"}},
		},
	})

	if err := s.UninstallSkill("claude-code"); err != nil {
		t.Fatalf("UninstallSkill claude-code: %v", err)
	}
	if _, err := os.Stat(pluginDir); !os.IsNotExist(err) {
		t.Fatalf("Claude plugin dir should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(claudeMarketplaceDir(home)); !os.IsNotExist(err) {
		t.Fatalf("Claude marketplace dir should be removed, stat err=%v", err)
	}

	installed := readJSONMap(t, filepath.Join(pluginsDir, "installed_plugins.json"))
	plugins := installed["plugins"].(map[string]any)
	opsctlEntries := plugins["opsctl@opskat"].([]any)
	if len(opsctlEntries) != 1 || opsctlEntries[0].(map[string]any)["scope"] != "project" {
		t.Fatalf("user scope should be removed and project scope kept: %#v", opsctlEntries)
	}
	if _, ok := plugins["other@market"]; !ok {
		t.Fatalf("unrelated plugin registration should remain")
	}

	marketplaces := readJSONMap(t, filepath.Join(pluginsDir, "known_marketplaces.json"))
	if _, ok := marketplaces["opskat"]; ok {
		t.Fatalf("opskat marketplace should be removed")
	}
	if _, ok := marketplaces["other"]; !ok {
		t.Fatalf("unrelated marketplace should remain")
	}

	settings := readJSONMap(t, settingsFile)
	if settings["theme"] != "dark" {
		t.Fatalf("unrelated settings should remain: %#v", settings)
	}
	enabled := settings["enabledPlugins"].(map[string]any)
	if _, ok := enabled["opsctl@opskat"]; ok {
		t.Fatalf("opsctl enabled plugin should be removed")
	}
	if enabled["other@market"] != true {
		t.Fatalf("unrelated enabled plugin should remain: %#v", enabled)
	}
	extra := settings["extraKnownMarketplaces"].(map[string]any)
	if _, ok := extra["opskat"]; ok {
		t.Fatalf("opskat extra marketplace should be removed")
	}
	if _, ok := extra["other"]; !ok {
		t.Fatalf("unrelated extra marketplace should remain")
	}
}

func TestUninstallSkillSkipsSymlinkTarget(t *testing.T) {
	home := setTestHome(t)
	s := New(t.Context(), SkillContent{})
	realTarget := filepath.Join(home, "real-opsctl")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatalf("MkdirAll realTarget: %v", err)
	}
	linkParent := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatalf("MkdirAll linkParent: %v", err)
	}
	linkTarget := filepath.Join(linkParent, "opsctl")
	if err := os.Symlink(realTarget, linkTarget); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlinks on Windows requires developer mode or elevated privileges: %v", err)
		}
		t.Fatalf("Symlink: %v", err)
	}

	// 开发模式：目标是软链接时跳过删除且不报错（与安装侧一致）。
	if err := s.UninstallSkill("codex"); err != nil {
		t.Fatalf("symlink target should be skipped, got error: %v", err)
	}
	if _, err := os.Stat(realTarget); err != nil {
		t.Fatalf("real target should not be removed: %v", err)
	}
	if _, err := os.Lstat(linkTarget); err != nil {
		t.Fatalf("symlink should be left in place: %v", err)
	}
}

func setTestHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	return home
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test helper reads temp files created by the test
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("Unmarshal %s: %v", path, err)
	}
	return value
}
