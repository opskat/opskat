package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609280001 为 groups 表添加 ext_policy 字段（组上各扩展策略面的策略）
func migration202609280001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609280001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`
				ALTER TABLE groups ADD COLUMN ext_policy TEXT
			`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			// SQLite 不支持 DROP COLUMN，需要重建表
			return nil
		},
	}
}
