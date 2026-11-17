package system

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/auto_backup_svc"
	"github.com/opskat/opskat/internal/service/backup_svc"
)

func newAutoBackupTestSystem(t *testing.T) *System {
	t.Helper()
	initBootstrapForSystemTest(t)
	if err := bootstrap.SaveConfig(&bootstrap.AppConfig{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := New(t.Context(), SkillContent{})
	s.emit = func(string, ...any) {}
	s.autoBackup = auto_backup_svc.New(auto_backup_svc.Options{
		Now:          time.Now,
		LoadSettings: func() (auto_backup_svc.Settings, error) { return auto_backup_svc.Settings{}, nil },
		Export: func(context.Context, auto_backup_svc.ClientSnapshot) (*backup_svc.BackupData, error) {
			return &backup_svc.BackupData{}, nil
		},
		Targets:   auto_backup_svc.Targets(),
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	})
	return s
}

func validAutoBackupInput(dir string) AutoBackupSaveInput {
	return AutoBackupSaveInput{
		Enabled:         true,
		Password:        "backup-pw",
		IntervalMinutes: 5,
		Keep:            30,
		Targets:         []string{"local"},
		LocalDir:        dir,
	}
}

func TestSaveAutoBackupSettingsValidation(t *testing.T) {
	s := newAutoBackupTestSystem(t)
	dir := t.TempDir()

	cases := []struct {
		name   string
		mutate func(*AutoBackupSaveInput)
		want   string
	}{
		{"没有密码", func(in *AutoBackupSaveInput) { in.Password = "" }, "密码"},
		{"没有勾选可用位置", func(in *AutoBackupSaveInput) { in.Targets = []string{"webdav"} }, "至少选择一个备份位置"},
		{"勾选本地但没选文件夹", func(in *AutoBackupSaveInput) { in.LocalDir = "" }, "文件夹"},
		{"间隔不在选项内", func(in *AutoBackupSaveInput) { in.IntervalMinutes = 7 }, "间隔"},
		{"份数不在选项内", func(in *AutoBackupSaveInput) { in.Keep = 3 }, "份数"},
		{"未知位置", func(in *AutoBackupSaveInput) { in.Targets = []string{"local", "ftp"} }, "ftp"},
	}
	for _, c := range cases {
		in := validAutoBackupInput(dir)
		c.mutate(&in)
		err := s.SaveAutoBackupSettings(in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
		if cfg := bootstrap.GetConfig(); cfg.AutoBackupEnabled || cfg.AutoBackupPassword != "" {
			t.Fatalf("%s: rejected save must not change config: %+v", c.name, cfg)
		}
	}
}

func TestSaveAutoBackupSettingsRoundTrip(t *testing.T) {
	s := newAutoBackupTestSystem(t)
	dir := t.TempDir()

	if err := s.SaveAutoBackupSettings(validAutoBackupInput(dir)); err != nil {
		t.Fatalf("SaveAutoBackupSettings: %v", err)
	}
	cfg := bootstrap.GetConfig()
	if cfg.AutoBackupPassword == "" || cfg.AutoBackupPassword == "backup-pw" {
		t.Fatalf("password must be stored encrypted, got %q", cfg.AutoBackupPassword)
	}

	got, err := s.GetAutoBackupSettings()
	if err != nil {
		t.Fatalf("GetAutoBackupSettings: %v", err)
	}
	want := AutoBackupSettings{Enabled: true, PasswordSet: true, IntervalMinutes: 5, Keep: 30, Targets: []string{"local"}, LocalDir: dir}
	if got.Enabled != want.Enabled || got.PasswordSet != want.PasswordSet || got.IntervalMinutes != want.IntervalMinutes ||
		got.Keep != want.Keep || strings.Join(got.Targets, ",") != "local" || got.LocalDir != want.LocalDir {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}

	// 留空密码保留已保存的密码；未保存过的设置读到默认值。
	in := validAutoBackupInput(dir)
	in.Password = ""
	if err := s.SaveAutoBackupSettings(in); err != nil {
		t.Fatalf("save with empty password: %v", err)
	}
	if bootstrap.GetConfig().AutoBackupPassword != cfg.AutoBackupPassword {
		t.Fatalf("empty password must keep the stored one")
	}
}

func TestGetAutoBackupSettingsDefaults(t *testing.T) {
	s := newAutoBackupTestSystem(t)
	got, err := s.GetAutoBackupSettings()
	if err != nil {
		t.Fatalf("GetAutoBackupSettings: %v", err)
	}
	if got.Enabled || got.PasswordSet || got.IntervalMinutes != 30 || got.Keep != 10 {
		t.Fatalf("defaults: %+v", got)
	}
}

func TestSaveAutoBackupKeepsUnavailableSelection(t *testing.T) {
	s := newAutoBackupTestSystem(t)
	dir := t.TempDir()
	in := validAutoBackupInput(dir)
	in.Targets = []string{"webdav", "local"}
	if err := s.SaveAutoBackupSettings(in); err != nil {
		t.Fatalf("SaveAutoBackupSettings: %v", err)
	}
	got, _ := s.GetAutoBackupSettings()
	if strings.Join(got.Targets, ",") != "webdav,local" {
		t.Fatalf("unavailable selection must be kept: %v", got.Targets)
	}

	st, err := s.GetAutoBackupStatus()
	if err != nil {
		t.Fatalf("GetAutoBackupStatus: %v", err)
	}
	if avail := availability(st); avail["webdav"] || avail["gist"] || !avail["local"] {
		t.Fatalf("availability: %+v", avail)
	}

	if err := bootstrap.UpdateConfig(func(c *bootstrap.AppConfig) {
		c.WebDAVURL = "https://dav.example.com/opskat/"
		c.GitHubToken = "enc"
		c.GitHubUser = "octocat"
	}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.GetAutoBackupStatus()
	if avail := availability(st); !avail["webdav"] || !avail["gist"] || st.WebDAVURL != "https://dav.example.com/opskat/" || st.GitHubUser != "octocat" {
		t.Fatalf("availability after configuring: %+v", st)
	}
}

func availability(st *AutoBackupStatus) map[string]bool {
	out := map[string]bool{}
	for _, t := range st.Targets {
		out[t.ID] = t.Available
	}
	return out
}
