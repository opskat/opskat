package mysql

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 整批执行时关闭外键检查：FOREIGN_KEY_CHECKS 是会话级的，只作用于批的那条连接。会话本来就没在
// 检查时什么都不做，结束后也不打开它。
var _ sqldb.ForeignKeyChecks = dialect{}

func (dialect) ForeignKeyChecksSQL(ctx context.Context, conn *sql.Conn, _, _ string) (string, string, error) {
	var enabled int
	if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.foreign_key_checks").Scan(&enabled); err != nil {
		return "", "", err
	}
	if enabled == 0 {
		return "", "", nil
	}
	return "SET FOREIGN_KEY_CHECKS = 0", "SET FOREIGN_KEY_CHECKS = 1", nil
}
