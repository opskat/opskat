package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610310003 给 audit_logs 添加 review 列（本次审核结果 JSON）。
// 归属方：审计（audit）。可重复执行。
func migration202610310003() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610310003",
		Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn("audit_logs", "review") {
				return nil
			}
			return tx.Exec("ALTER TABLE audit_logs ADD COLUMN review TEXT DEFAULT ''").Error
		},
	}
}
