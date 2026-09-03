package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 执行数据库迁移。
// dataDir 是应用实际使用的数据目录（可能被 --data-dir 覆盖或为便携目录），
// 供需要读取磁盘旧配置的迁移使用。
func RunMigrations(db *gorm.DB, dataDir string) error {
	m := gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{
		migration202603220001(),
		migration202603260001(dataDir),
		migration202603270001(),
		migration202603290001(),
		migration202603300001(),
		migration202603300002(),
		migration202603310001(),
		migration202604050001(),
		migration202604140001(),
		migration202604160001(),
		migration202604170001(),
		migration202604220001(),
		migration202604230001(),
		migration202604270001(),
		migration202605010001(),
		migration202605060001(),
		migration202605070001(),
		migration202605120001(),
		migration202605260001(),
		migration202607310001(),
		migration202608040001(),
		migration202609030001(),
	})
	return m.Migrate()
}
