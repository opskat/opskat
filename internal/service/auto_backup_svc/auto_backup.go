package auto_backup_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

const scheduleDelay = 2 * time.Second

type Service struct {
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	timer        *time.Timer
	running      bool
	pending      bool
	shortcuts    string
	customThemes string
}

var defaultService = New()

var (
	exportData             = backup_svc.Export
	uploadWebDAVAutoBackup = backup_svc.CreateOrUpdateWebDAVAutoBackup
	delay                  = scheduleDelay
	ready                  = autoBackupReady
	loadAutoBackupConfig   = webDAVAutoBackupConfig
	record                 = recordResult
)

func New() *Service {
	return &Service{}
}

func Default() *Service {
	return defaultService
}

func Start(ctx context.Context) {
	defaultService.Start(ctx)
}

func Stop() {
	defaultService.Stop()
}

func Schedule() {
	defaultService.Schedule()
}

func SetClientSnapshot(shortcuts, customThemes string) error {
	return defaultService.SetClientSnapshot(shortcuts, customThemes)
}

func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.running = false
	s.pending = false
}

func (s *Service) Schedule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil || !ready() {
		return
	}
	if s.running {
		s.pending = true
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(delay, s.run)
}

func (s *Service) SetClientSnapshot(shortcuts, customThemes string) error {
	if err := validateSnapshotJSON(shortcuts, "shortcuts"); err != nil {
		return err
	}
	if err := validateSnapshotJSON(customThemes, "custom themes"); err != nil {
		return err
	}
	s.mu.Lock()
	s.shortcuts = shortcuts
	s.customThemes = customThemes
	s.mu.Unlock()
	s.Schedule()
	return nil
}

func validateSnapshotJSON(raw, name string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return fmt.Errorf("invalid %s JSON", name)
	}
	return nil
}

func autoBackupReady() bool {
	cfg := bootstrap.GetConfig()
	return cfg != nil && cfg.WebDAVAutoBackupEnabled && strings.TrimSpace(cfg.WebDAVURL) != "" && cfg.WebDAVAutoBackupPassword != ""
}

func (s *Service) run() {
	s.mu.Lock()
	if s.ctx == nil || !ready() {
		s.timer = nil
		s.running = false
		s.pending = false
		s.mu.Unlock()
		return
	}
	s.timer = nil
	s.running = true
	s.pending = false
	ctx := s.ctx
	shortcuts := s.shortcuts
	customThemes := s.customThemes
	s.mu.Unlock()

	err := runOnce(ctx, shortcuts, customThemes)
	if err != nil {
		logger.Default().Warn("webdav auto backup failed", zap.Error(err))
		record(0, err)
	} else {
		logger.Default().Info("webdav auto backup completed")
		record(time.Now().Unix(), nil)
	}

	s.mu.Lock()
	s.running = false
	pending := s.pending
	s.pending = false
	s.mu.Unlock()
	if pending {
		s.Schedule()
	}
}

func runOnce(ctx context.Context, shortcuts, customThemes string) error {
	cfg, password, err := loadAutoBackupConfig()
	if err != nil {
		return err
	}
	opts := &backup_svc.ExportOptions{
		IncludeCredentials:  true,
		IncludeForwards:     true,
		IncludePolicyGroups: true,
		Shortcuts:           shortcuts,
		CustomThemes:        customThemes,
	}
	data, err := exportData(ctx, opts, credential_svc.Default())
	if err != nil {
		return err
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	encrypted, err := backup_svc.EncryptBackup(jsonData, password)
	if err != nil {
		return err
	}
	_, err = uploadWebDAVAutoBackup(cfg, encrypted)
	return err
}

func webDAVAutoBackupConfig() (backup_svc.WebDAVConfig, string, error) {
	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return backup_svc.WebDAVConfig{}, "", fmt.Errorf("config not loaded")
	}
	if !cfg.WebDAVAutoBackupEnabled {
		return backup_svc.WebDAVConfig{}, "", fmt.Errorf("WebDAV auto backup is disabled")
	}
	if strings.TrimSpace(cfg.WebDAVURL) == "" {
		return backup_svc.WebDAVConfig{}, "", fmt.Errorf("WebDAV 未配置")
	}
	password, err := credential_svc.Default().Decrypt(cfg.WebDAVAutoBackupPassword)
	if err != nil {
		return backup_svc.WebDAVConfig{}, "", fmt.Errorf("解密 WebDAV 自动备份密码失败: %w", err)
	}
	authType := backup_svc.WebDAVAuthType(cfg.WebDAVAuthType)
	if authType == "" {
		authType = backup_svc.WebDAVAuthNone
	}
	out := backup_svc.WebDAVConfig{
		URL:      cfg.WebDAVURL,
		AuthType: authType,
		Username: cfg.WebDAVUsername,
	}
	if cfg.WebDAVPassword != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVPassword)
		if err != nil {
			return backup_svc.WebDAVConfig{}, "", fmt.Errorf("解密 WebDAV 密码失败: %w", err)
		}
		out.Password = decrypted
	}
	if cfg.WebDAVToken != "" {
		decrypted, err := credential_svc.Default().Decrypt(cfg.WebDAVToken)
		if err != nil {
			return backup_svc.WebDAVConfig{}, "", fmt.Errorf("解密 WebDAV token 失败: %w", err)
		}
		out.Token = decrypted
	}
	return out, password, nil
}

func recordResult(successAt int64, err error) {
	if updateErr := bootstrap.UpdateConfig(func(cfg *bootstrap.AppConfig) {
		// 若期间用户已关闭/清除了自动备份（ClearWebDAVConfig / 关闭开关），
		// 不要把这次进行中备份的过期状态写回已清空的配置。
		if !cfg.WebDAVAutoBackupEnabled {
			return
		}
		if err != nil {
			cfg.WebDAVAutoBackupLastError = truncateError(err.Error())
		} else {
			cfg.WebDAVAutoBackupLastAt = successAt
			cfg.WebDAVAutoBackupLastError = ""
		}
	}); updateErr != nil {
		logger.Default().Warn("save webdav auto backup status", zap.Error(updateErr))
	}
}

func truncateError(msg string) string {
	const max = 500
	if len(msg) <= max {
		return msg
	}
	return msg[:max]
}
