package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610310001 创建命令审核结果缓存表 command_reviews。
// 归属方：命令审核（command_review_svc）。可重复执行。
func migration202610310001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610310001",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS command_reviews (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					cache_key VARCHAR(64) NOT NULL,
					result TEXT NOT NULL,
					createtime INTEGER,
					expiretime INTEGER
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uq_command_reviews_cache_key ON command_reviews(cache_key)`,
				`CREATE INDEX IF NOT EXISTS idx_command_reviews_expiretime ON command_reviews(expiretime)`,
			}
			for _, stmt := range stmts {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
