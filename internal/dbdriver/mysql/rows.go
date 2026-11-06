package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 行语句：反斜杠在文本字面量里是不是转义符取决于会话的 sql_mode，每次生成前在这条连接上查；二进制列里
// 表格带回的文本与改动前一样照字符串写，只有导入按二进制解码出的字节写成 X'…'；只改一行用 LIMIT 1。
var _ sqldb.RowWriter = dialect{}

func (dialect) Literals(ctx context.Context, conn *sql.Conn) (sqldb.Literals, error) {
	var mode string
	if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&mode); err != nil {
		return sqldb.Literals{}, fmt.Errorf("query sql_mode: %w", err)
	}
	return sqldb.Literals{
		BackslashEscapes: !strings.Contains(strings.ToUpper(mode), "NO_BACKSLASH_ESCAPES"),
		Binary: func(columnType string) (string, string, bool) {
			return "X'", "'", isBinaryType(columnType)
		},
		BinaryBytesOnly: true,
	}, nil
}

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s LIMIT 1;", tableRef, set, where)
}

// 表格编辑：一列都没填的新增行写 () VALUES ()；只删一行同样用 LIMIT 1。MySQL 没有物理行标识，
// 没有主键时只能在原值相同的几行里改 / 删第一行。
var _ sqldb.RowEditor = dialect{}

func (dialect) InsertRowSQL(tableRef, columns, values string, _ bool) string {
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tableRef, columns, values)
}

func (dialect) DeleteRowSQL(tableRef, where string) string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s LIMIT 1;", tableRef, where)
}

func (dialect) ExactRow(string, string) (string, bool) { return "", false }
