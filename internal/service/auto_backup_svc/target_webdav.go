package auto_backup_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// webdavTarget 复用「WebDAV 备份」卡片里配置的连接。
type webdavTarget struct{}

func init() { RegisterTarget(webdavTarget{}) }

func (webdavTarget) ID() string { return "webdav" }

func (webdavTarget) Validate(Settings) error { return nil }

func (webdavTarget) Available(Settings) bool {
	cfg := bootstrap.GetConfig()
	return cfg != nil && cfg.WebDAVURL != ""
}

func (webdavTarget) Write(_ context.Context, req WriteRequest) (string, error) {
	cfg, err := backup_svc.StoredWebDAVConfig(credential_svc.Default())
	if err != nil {
		return "", err
	}
	if _, err := backup_svc.UploadWebDAVAutoBackup(cfg, req.Content, req.At); err != nil {
		return "", err
	}
	// 清理失败不算本次失败：新版本已经写好，旧版本多留几份无害。
	if err := backup_svc.PruneWebDAVAutoBackups(cfg, req.Keep); err != nil {
		logger.Default().Warn("auto backup: prune WebDAV", zap.Error(err))
	}
	return "", nil
}
