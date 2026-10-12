package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610120001 把 202603290001 之后才写进库的数字权限组 ID 再转一遍。
//
// 202603290001 只改得到它执行那一刻库里的行。数字 ID 之后仍可能进库：v1.1.1 及更早版本的
// 程序在已迁移的库上继续写入，或导入了那些版本导出的备份。带数字 ID 的策略整条解析不了，
// 而策略的写入边界现在会拒收解析不了的值——不修掉这些行，它们所在的资产/分组就再也改不动。
func migration202610120001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610120001",
		Migrate: func(tx *gorm.DB) error {
			return migration202603290001().Migrate(tx)
		},
	}
}
