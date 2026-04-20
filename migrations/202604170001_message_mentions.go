package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202604170001 为 conversation_messages 表添加 mentions 字段
func migration202604170001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202604170001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`
				ALTER TABLE conversation_messages ADD COLUMN mentions TEXT
			`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			// SQLite 不支持 DROP COLUMN
			return nil
		},
	}
}
