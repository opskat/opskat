package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 行语句：nchar / nvarchar / ntext 列的文本带 N 前缀（没有它，库排序规则不是 Unicode 时中文等
// 字符会被写成 ?），二进制写成 0x…；T-SQL 没有 LIMIT，只改一行用 UPDATE TOP (1)。
var _ sqldb.RowWriter = dialect{}

var literals = sqldb.Literals{
	UnicodeText: func(columnType string) bool {
		switch strings.ToLower(columnType) {
		case "nchar", "nvarchar", "ntext":
			return true
		}
		return false
	},
	Binary: func(columnType string) (string, string, bool) {
		switch strings.ToLower(columnType) {
		case "binary", "varbinary", "image":
			return "0x", "", true
		}
		return "", "", false
	},
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) { return literals, nil }

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE TOP (1) %s SET %s WHERE %s;", tableRef, set, where)
}
