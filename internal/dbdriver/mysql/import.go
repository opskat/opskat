package mysql

import (
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 导入：追加/更新用 ON DUPLICATE KEY UPDATE，追加/跳过用 INSERT IGNORE。列的类别按 SHOW FULL
// COLUMNS 给出的类型名（括号与 unsigned 等修饰之前的部分）。
var _ sqldb.Importer = dialect{}

// typeName 是 SHOW FULL COLUMNS 的类型去掉长度与修饰后的类型名（"int(11) unsigned" → "int"）。
func typeName(columnType string) string {
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(columnType)), "(")
	name, _, _ = strings.Cut(name, " ")
	return name
}

func (dialect) ColumnCategory(columnType string) dbdriver.ColumnCategory {
	switch typeName(columnType) {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "float", "double", "real", "decimal", "numeric":
		return dbdriver.CategoryNumber
	case "date":
		return dbdriver.CategoryDate
	case "datetime", "timestamp":
		return dbdriver.CategoryDateTime
	}
	if isBinaryType(columnType) {
		return dbdriver.CategoryBinary
	}
	return dbdriver.CategoryText
}

// isBinaryType 报告这种列是二进制（写成 X'…'）。
func isBinaryType(columnType string) bool {
	switch typeName(columnType) {
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return true
	}
	return false
}

func (dialect) ImportInsertSQL(ins sqldb.ImportInsert) []string {
	switch ins.Conflict {
	case sqldb.ConflictSkip:
		return ins.Values(ins.Head("INSERT IGNORE INTO")+" VALUES", "")
	case sqldb.ConflictUpdate:
		set := make([]string, len(ins.Updates))
		for i, column := range ins.Updates {
			set[i] = column + " = VALUES(" + column + ")"
		}
		return ins.Values(ins.Head("INSERT INTO")+" VALUES", " ON DUPLICATE KEY UPDATE "+strings.Join(set, ", "))
	}
	return ins.Values(ins.Head("INSERT INTO")+" VALUES", "")
}
