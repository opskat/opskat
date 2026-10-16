package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 行语句：声明类型含 BLOB 的列（BLOB 亲和性）写成 X'…'；按主键或全部列定位的 UPDATE 不另加限制。
var _ sqldb.RowWriter = dialect{}

var literals = sqldb.Literals{
	Binary: func(columnType string) (string, string, bool) {
		if strings.Contains(strings.ToUpper(columnType), "BLOB") {
			return "X'", "'", true
		}
		return "", "", false
	},
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) { return literals, nil }

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s;", tableRef, set, where)
}
