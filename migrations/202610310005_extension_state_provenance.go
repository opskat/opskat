package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610310005 给 extension_state 增加安装来源（store / local / dev）与包摘要
// （wasm 与 manifest.json 的 sha256）。已有行两列为空：来源未知，不能当作商店安装。
// 归属方：扩展管理（extension_svc）。可重复执行：已有该列（开发库跑过旧 ID）时跳过。
func migration202610310005() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610310005",
		Migrate: func(tx *gorm.DB) error {
			columns := []struct{ column, ddl string }{
				{"source", "ALTER TABLE extension_state ADD COLUMN source VARCHAR(16) DEFAULT ''"},
				{"package_sha256", "ALTER TABLE extension_state ADD COLUMN package_sha256 VARCHAR(64) DEFAULT ''"},
			}
			for _, c := range columns {
				if tx.Migrator().HasColumn("extension_state", c.column) {
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
