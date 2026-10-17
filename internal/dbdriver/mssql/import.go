package mssql

import (
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 导入：T-SQL 没有 INSERT IGNORE / ON CONFLICT / ON DUPLICATE KEY，追加/更新与追加/跳过每行一条
// MERGE（跳过时只有 NOT MATCHED 分支）；映射里有 IDENTITY 列时每条插入类语句都包上
// IDENTITY_INSERT 的开关，没有时不碰它。
var _ sqldb.Importer = dialect{}

func (dialect) ColumnCategory(columnType string) dbdriver.ColumnCategory {
	switch strings.ToLower(columnType) {
	case "tinyint", "smallint", "int", "bigint", "decimal", "numeric", "float", "real", "money", "smallmoney":
		return dbdriver.CategoryNumber
	case "date":
		return dbdriver.CategoryDate
	case "datetime", "datetime2", "smalldatetime", "datetimeoffset":
		return dbdriver.CategoryDateTime
	}
	if _, _, ok := literals.Binary(columnType); ok {
		return dbdriver.CategoryBinary
	}
	return dbdriver.CategoryText
}

func (dialect) ImportInsertSQL(ins sqldb.ImportInsert) []string {
	var statements []string
	if ins.Conflict == sqldb.ConflictNone {
		statements = ins.Values(ins.Head("INSERT INTO")+" VALUES", "")
	} else {
		statements = make([]string, len(ins.Rows))
		for i, row := range ins.Rows {
			statements[i] = mergeSQL(ins, row)
		}
	}
	if ins.Identity {
		for i, statement := range statements {
			statements[i] = identityInsert(ins.TableRef, statement)
		}
	}
	return statements
}

// mergeSQL 是插入一行、键已存在时更新（ConflictUpdate）或跳过（ConflictSkip）的 MERGE。
func mergeSQL(ins sqldb.ImportInsert, row []string) string {
	source := make([]string, len(ins.Columns))
	values := make([]string, len(ins.Columns))
	for i, column := range ins.Columns {
		source[i] = row[i] + " AS " + column
		values[i] = "s." + column
	}
	on := make([]string, len(ins.Keys))
	for i, key := range ins.Keys {
		on[i] = "t." + key + " = s." + key
	}
	matched := ""
	if ins.Conflict == sqldb.ConflictUpdate {
		set := make([]string, len(ins.Updates))
		for i, column := range ins.Updates {
			set[i] = "t." + column + " = s." + column
		}
		matched = "WHEN MATCHED THEN UPDATE SET " + strings.Join(set, ", ") + " "
	}
	return fmt.Sprintf("MERGE %s AS t USING (SELECT %s) AS s ON (%s) %sWHEN NOT MATCHED THEN INSERT (%s) VALUES (%s);",
		ins.TableRef, strings.Join(source, ", "), strings.Join(on, " AND "), matched,
		strings.Join(ins.Columns, ", "), strings.Join(values, ", "))
}
