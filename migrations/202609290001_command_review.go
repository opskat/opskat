package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609290001 为模型审核（辅助审批 / Autopilot）建表加列：
//   - command_reviews：审核结果缓存；
//   - assets / groups.permission_mode：权限模式，空表示沿用上级分组或默认；
//   - audit_logs.review：本次审核结果 JSON。
func migration202609290001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609290001",
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

			columns := []struct{ table, column, ddl string }{
				{"assets", "permission_mode", "ALTER TABLE assets ADD COLUMN permission_mode VARCHAR(20) DEFAULT ''"},
				{"groups", "permission_mode", "ALTER TABLE groups ADD COLUMN permission_mode VARCHAR(20) DEFAULT ''"},
				{"audit_logs", "review", "ALTER TABLE audit_logs ADD COLUMN review TEXT DEFAULT ''"},
			}
			for _, c := range columns {
				if tx.Migrator().HasColumn(c.table, c.column) {
					continue
				}
				if err := tx.Exec(c.ddl).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
