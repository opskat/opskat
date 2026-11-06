package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
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

// 表格编辑：一列都没填的新增行写 DEFAULT VALUES；给 IDENTITY 列写值要临时打开这张表的
// IDENTITY_INSERT，写完关闭，出错也关闭并原样抛出（都在同一条语句里，不留会话状态）；只删一行用
// DELETE TOP (1)。SQL Server 没有物理行标识，没有主键时只能在原值相同的几行里改 / 删其中一行。
var _ sqldb.RowEditor = dialect{}

func (dialect) InsertRowSQL(tableRef, columns, values string, identity bool) string {
	if columns == "" {
		return fmt.Sprintf("INSERT INTO %s DEFAULT VALUES;", tableRef)
	}
	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tableRef, columns, values)
	if !identity {
		return insert
	}
	return identityInsert(tableRef, insert)
}

// identityInsert 让 statement 能给这张表的 IDENTITY 列写值：临时打开 IDENTITY_INSERT，写完关闭，
// 出错也关闭并原样抛出；都在同一条语句里，不留会话状态，也不赋值变量（SQL Server 把变量赋值计为
// 1 行受影响）。
func identityInsert(tableRef, statement string) string {
	return fmt.Sprintf("SET IDENTITY_INSERT %[1]s ON; BEGIN TRY %[2]s END TRY "+
		"BEGIN CATCH SET IDENTITY_INSERT %[1]s OFF; THROW; END CATCH; SET IDENTITY_INSERT %[1]s OFF;", tableRef, statement)
}

func (dialect) DeleteRowSQL(tableRef, where string) string {
	return fmt.Sprintf("DELETE TOP (1) FROM %s WHERE %s;", tableRef, where)
}

func (dialect) ExactRow(string, string) (string, bool) { return "", false }
