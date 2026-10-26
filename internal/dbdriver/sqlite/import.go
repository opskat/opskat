package sqlite

import (
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 导入：追加/跳过用 INSERT OR IGNORE，追加/更新用 ON CONFLICT (键) DO UPDATE。SQLite 的列只有声明
// 类型，类别按亲和性规则判断（含 INT 是整数，含 CHAR / CLOB / TEXT 是文本，含 BLOB 是二进制，含
// REAL / FLOA / DOUBLE 是浮点），日期与日期时间按常见的声明名。
var _ sqldb.Importer = dialect{}

func (dialect) ColumnCategory(columnType string) dbdriver.ColumnCategory {
	t := strings.ToUpper(strings.TrimSpace(columnType))
	name, _, _ := strings.Cut(t, "(")
	name = strings.TrimSpace(name)
	containsAny := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(t, part) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("INT"):
		return dbdriver.CategoryNumber
	case containsAny("CHAR", "CLOB", "TEXT"):
		return dbdriver.CategoryText
	case containsAny("BLOB"):
		return dbdriver.CategoryBinary
	case containsAny("REAL", "FLOA", "DOUBLE"):
		return dbdriver.CategoryNumber
	case name == "DATE":
		return dbdriver.CategoryDate
	case name == "DATETIME" || name == "TIMESTAMP":
		return dbdriver.CategoryDateTime
	case containsAny("NUMERIC", "DECIMAL"):
		return dbdriver.CategoryNumber
	}
	return dbdriver.CategoryText
}

func (dialect) ImportInsertSQL(ins sqldb.ImportInsert) []string {
	switch ins.Conflict {
	case sqldb.ConflictSkip:
		return ins.Values(ins.Head("INSERT OR IGNORE INTO")+" VALUES", "")
	case sqldb.ConflictUpdate:
		return ins.Values(ins.Head("INSERT INTO")+" VALUES", ins.OnConflictDoUpdate())
	}
	return ins.Values(ins.Head("INSERT INTO")+" VALUES", "")
}
