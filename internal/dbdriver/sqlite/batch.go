package sqlite

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 整批执行时关闭外键检查：PRAGMA foreign_keys 作用于这条连接，在事务里切换不起作用——sqldb
// 在事务之外执行它。SQLite 默认不检查外键：连接本来就没在检查时什么都不做，结束后也不打开它。
var _ sqldb.ForeignKeyChecks = dialect{}

func (dialect) ForeignKeyChecksSQL(ctx context.Context, conn *sql.Conn, _, _ string) (string, string, error) {
	var enabled int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return "", "", err
	}
	if enabled == 0 {
		return "", "", nil
	}
	return "PRAGMA foreign_keys = OFF", "PRAGMA foreign_keys = ON", nil
}
