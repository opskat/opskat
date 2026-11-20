package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 行语句：nchar / nvarchar / ntext 列的文本带 N 前缀（没有它，库排序规则不是 Unicode 时中文等
// 字符会被写成 ?），二进制写成 0x…，读出的 uniqueidentifier（16 个原始字节）写成 GUID 文本；T-SQL 没有
// LIMIT，只改一行用 UPDATE TOP (1)。
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
	Formatted: func(columnType, text string) (string, bool) {
		if !strings.EqualFold(columnType, "uniqueidentifier") || len(text) != guidBytes {
			return "", false
		}
		return "'" + guidText([]byte(text)) + "'", true
	},
}

// guidBytes 是 uniqueidentifier 的字节数；驱动读出的就是这 16 个字节，表格与导入带来的 GUID 文本是 36 或 38 个字符。
const guidBytes = 16

// guidText 把 uniqueidentifier 的 16 个原始字节写成 SQL Server 显示的 GUID 文本：前三段（4、2、2 个字节）
// 按小端存放、倒过来写，其余按次序。
func guidText(b []byte) string {
	order := []int{3, 2, 1, 0, -1, 5, 4, -1, 7, 6, -1, 8, 9, -1, 10, 11, 12, 13, 14, 15}
	var out strings.Builder
	for _, i := range order {
		if i < 0 {
			out.WriteByte('-')
			continue
		}
		fmt.Fprintf(&out, "%02X", b[i])
	}
	return out.String()
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
