package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608040001 创建 ssh_agent_sources 表，持久化 SSH Agent 来源定义。
// 表只保存端点定义（端点类型 + 端点值）+ 显示元数据；身份、公钥、签名、私钥和
// 运行时状态永不落库。
func migration202608040001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608040001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`
				CREATE TABLE IF NOT EXISTS ssh_agent_sources (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					name TEXT NOT NULL,
					endpoint_type TEXT NOT NULL,
					endpoint TEXT NOT NULL,
					description TEXT,
					createtime INTEGER NOT NULL,
					updatetime INTEGER NOT NULL
				)
			`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS ssh_agent_sources`).Error
		},
	}
}
