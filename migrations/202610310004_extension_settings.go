package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610310004 创建扩展设置表 extension_settings：每个扩展一行，值为设置 JSON
// （format:"password" 字段为密文）。设置不再借用扩展自己的 KV，扩展经 KV 读写不到它。
// 归属方：扩展管理（extension_svc）。可重复执行。
func migration202610310004() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610310004",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS extension_settings (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					extension_name VARCHAR(255) NOT NULL,
					value TEXT NOT NULL,
					createtime INTEGER,
					updatetime INTEGER
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uq_extension_settings_extension_name ON extension_settings(extension_name)`,
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
