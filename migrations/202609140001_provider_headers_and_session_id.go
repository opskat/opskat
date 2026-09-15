package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func migration202609140001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609140001",
		Migrate: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("ai_providers", "extra_headers") {
				if err := tx.Exec("ALTER TABLE ai_providers ADD COLUMN extra_headers TEXT DEFAULT ''").Error; err != nil {
					return err
				}
			}

			// 老会话留空，首次取用时由 conversation_svc.EnsureExternalSessionID 补生成。
			if !tx.Migrator().HasColumn("conversations", "external_session_id") {
				if err := tx.Exec("ALTER TABLE conversations ADD COLUMN external_session_id VARCHAR(64) DEFAULT ''").Error; err != nil {
					return err
				}
			}

			return nil
		},
	}
}
