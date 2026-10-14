package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610040001 创建 custom_types 表，持久化自定义类型定义（字段结构、
// 执行方式与绑定模板、使用说明、默认策略）。字段值不在这里：通用资产把值存在
// 自己的 assets.config 里，并以 slug 引用类型。
func migration202610040001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610040001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS custom_types (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					slug VARCHAR(64) NOT NULL,
					name VARCHAR(255) NOT NULL,
					icon VARCHAR(100),
					exec_mode VARCHAR(20) NOT NULL,
					fields TEXT,
					http_config TEXT,
					command_config TEXT,
					usage TEXT,
					default_policy TEXT,
					createtime INTEGER NOT NULL,
					updatetime INTEGER NOT NULL
				)
			`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_types_slug ON custom_types(slug)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS custom_types`).Error
		},
	}
}
