// Package auto_backup_svc 按间隔把加密的完整备份写到用户勾选的备份位置（WebDAV / GitHub Gist /
// 本地文件夹），数据未变化时跳过；见 docs/specs/2026-11-17-auto-backup-targets.md。
package auto_backup_svc

import (
	"fmt"
	"slices"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
)

// 可选的检查间隔（分钟）与保留份数；与设置页的下拉选项一一对应。
var (
	IntervalChoices = []int{5, 30, 60, 1440}
	KeepChoices     = []int{5, 10, 30}
)

const (
	defaultIntervalMinutes = 30
	defaultKeep            = 10
)

// Settings 是生效中的自动备份设置（密码已解密）。
type Settings struct {
	Enabled  bool
	Password string
	Interval time.Duration
	Keep     int
	Targets  []string
	LocalDir string
}

// Selected 报告 id 位置是否被勾选。
func (s Settings) Selected(id string) bool { return slices.Contains(s.Targets, id) }

// LoadSettings 从 config.json 读取自动备份设置；未设置的间隔 / 份数取默认值。
func LoadSettings(crypto backup_svc.CredentialCrypto) (Settings, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return Settings{}, fmt.Errorf("config not loaded")
	}
	out := Settings{
		Enabled:  cfg.AutoBackupEnabled,
		Interval: time.Duration(IntervalMinutesOrDefault(cfg.AutoBackupIntervalMinutes)) * time.Minute,
		Keep:     KeepOrDefault(cfg.AutoBackupKeep),
		Targets:  cfg.AutoBackupTargets,
		LocalDir: cfg.AutoBackupLocalDir,
	}
	if cfg.AutoBackupPassword != "" {
		pw, err := crypto.Decrypt(cfg.AutoBackupPassword)
		if err != nil {
			return Settings{}, fmt.Errorf("解密自动备份密码失败: %w", err)
		}
		out.Password = pw
	}
	return out, nil
}

// IntervalMinutesOrDefault 把未设置（0）的间隔换成默认值。
func IntervalMinutesOrDefault(v int) int {
	if v == 0 {
		return defaultIntervalMinutes
	}
	return v
}

// KeepOrDefault 把未设置（0）的保留份数换成默认值。
func KeepOrDefault(v int) int {
	if v == 0 {
		return defaultKeep
	}
	return v
}
