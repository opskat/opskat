package migrations

import (
	"ops-cat/internal/model/entity/asset_entity"
	"ops-cat/internal/model/entity/group_entity"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 执行数据库迁移
func RunMigrations(db *gorm.DB) error {
	m := gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{
		{
			ID: "202603220001",
			Migrate: func(tx *gorm.DB) error {
				if err := tx.AutoMigrate(&asset_entity.Asset{}); err != nil {
					return err
				}
				if err := tx.AutoMigrate(&group_entity.Group{}); err != nil {
					return err
				}
				return nil
			},
			Rollback: func(tx *gorm.DB) error {
				if err := tx.Migrator().DropTable("assets"); err != nil {
					return err
				}
				return tx.Migrator().DropTable("groups")
			},
		},
	})
	return m.Migrate()
}
