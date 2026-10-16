package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 行语句：反斜杠在文本字面量里是不是转义符取决于会话的 sql_mode，每次生成前在这条连接上查；
// 只改一行用 LIMIT 1。
var _ sqldb.RowWriter = dialect{}

func (dialect) Literals(ctx context.Context, conn *sql.Conn) (sqldb.Literals, error) {
	var mode string
	if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&mode); err != nil {
		return sqldb.Literals{}, fmt.Errorf("query sql_mode: %w", err)
	}
	return sqldb.Literals{
		BackslashEscapes: !strings.Contains(strings.ToUpper(mode), "NO_BACKSLASH_ESCAPES"),
	}, nil
}

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s LIMIT 1;", tableRef, set, where)
}
