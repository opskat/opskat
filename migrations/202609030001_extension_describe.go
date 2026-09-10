package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func migration202609030001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609030001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS extension_describe (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				name       VARCHAR(255) NOT NULL,
				wasm_hash  VARCHAR(64) NOT NULL,
				descriptor TEXT NOT NULL,
				createtime INTEGER NOT NULL,
				updatetime INTEGER NOT NULL
			)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_ext_describe_name ON extension_describe (name)`).Error
		},
	}
}
