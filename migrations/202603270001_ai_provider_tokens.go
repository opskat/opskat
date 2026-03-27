package migrations

import (
	"github.com/opskat/opskat/internal/status"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/go-gormigrate/gormigrate/v2"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func migration202603270001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202603270001",
		Migrate: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("ai_providers", "max_output_tokens") {
				if err := tx.Exec("ALTER TABLE ai_providers ADD COLUMN max_output_tokens INTEGER DEFAULT 0").Error; err != nil {
					logger.Default().Warn("migration 202603270001: 添加 ai_providers.max_output_tokens 列失败", zap.Error(err))
					status.Add(status.Entry{
						Level:   status.LevelWarn,
						Source:  "migration",
						Message: "添加 ai_providers.max_output_tokens 列失败",
						Detail:  err.Error(),
					})
				}
			}
			if !tx.Migrator().HasColumn("ai_providers", "context_window") {
				if err := tx.Exec("ALTER TABLE ai_providers ADD COLUMN context_window INTEGER DEFAULT 0").Error; err != nil {
					logger.Default().Warn("migration 202603270001: 添加 ai_providers.context_window 列失败", zap.Error(err))
					status.Add(status.Entry{
						Level:   status.LevelWarn,
						Source:  "migration",
						Message: "添加 ai_providers.context_window 列失败",
						Detail:  err.Error(),
					})
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return nil
		},
	}
}
