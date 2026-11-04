package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610310002 给 assets / groups 添加 permission_mode 列（空表示沿用上级分组或默认）。
// 归属方：权限（permission）。可重复执行。
func migration202610310002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610310002",
		Migrate: func(tx *gorm.DB) error {
			for _, table := range []string{"assets", "groups"} {
				if tx.Migrator().HasColumn(table, "permission_mode") {
					continue
				}
				if err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN permission_mode VARCHAR(20) DEFAULT ''").Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
