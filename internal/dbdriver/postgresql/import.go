package postgresql

import (
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 导入：追加/更新与追加/跳过用 ON CONFLICT (键)；映射里有 GENERATED ALWAYS 列时插入带 OVERRIDING
// SYSTEM VALUE。列的类别按 information_schema 的 data_type。
var _ sqldb.Importer = dialect{}

func (dialect) ColumnCategory(columnType string) dbdriver.ColumnCategory {
	switch strings.ToLower(columnType) {
	case "smallint", "integer", "bigint", "numeric", "decimal", "real", "double precision":
		return dbdriver.CategoryNumber
	case "date":
		return dbdriver.CategoryDate
	case "timestamp without time zone", "timestamp with time zone":
		return dbdriver.CategoryDateTime
	}
	if _, _, ok := literals.Binary(columnType); ok {
		return dbdriver.CategoryBinary
	}
	return dbdriver.CategoryText
}

func (dialect) ImportInsertSQL(ins sqldb.ImportInsert) []string {
	prefix := ins.Head("INSERT INTO")
	if ins.Identity {
		prefix += " OVERRIDING SYSTEM VALUE"
	}
	return ins.Values(prefix+" VALUES", onConflict(ins))
}

// onConflict 是键已存在时的处理；不处理冲突时为空串。
func onConflict(ins sqldb.ImportInsert) string {
	switch ins.Conflict {
	case sqldb.ConflictSkip:
		return " ON CONFLICT (" + strings.Join(ins.Keys, ", ") + ") DO NOTHING"
	case sqldb.ConflictUpdate:
		return ins.OnConflictDoUpdate()
	}
	return ""
}
