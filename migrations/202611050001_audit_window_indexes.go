package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611050001 给 audit_logs 建 (session_id, createtime) 与 (source, createtime)
// 组合索引，并 ANALYZE 让规划器拿到统计。审计页的 ListSessions（按 createtime 窗口
// GROUP BY session_id）与带 source 过滤的 List 在大表上原先走 idx_audit_session_id /
// idx_audit_source 整条扫描；有统计后窗口查询改走时间索引范围查找。
// 归属方：审计（audit_repo）。可重复执行（IF NOT EXISTS）。
func migration202611050001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611050001",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE INDEX IF NOT EXISTS idx_audit_session_createtime ON audit_logs (session_id, createtime)`,
				`CREATE INDEX IF NOT EXISTS idx_audit_source_createtime ON audit_logs (source, createtime)`,
				`ANALYZE audit_logs`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
