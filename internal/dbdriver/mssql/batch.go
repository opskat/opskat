package mssql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 整批执行时关闭外键检查：SQL Server 没有会话级的开关，停用的是批写入的那张表上当前启用着的外键
// 约束（CHECK 约束与原本就停用的外键不碰）；结束后只把这些外键 WITH NOCHECK 重新启用——不校验
// 表里已有的数据，导入了违反外键的行时约束照样恢复为启用（标记为不受信任），与 MySQL / SQLite
// 恢复外键检查时不回查已有行一致。
var _ sqldb.ForeignKeyChecks = dialect{}

func (d dialect) ForeignKeyChecksSQL(ctx context.Context, conn *sql.Conn, database, table string) (string, string, error) {
	ref := d.TableRef(database, table)
	names, err := sqldb.QueryNames(ctx, conn,
		"SELECT name FROM sys.foreign_keys WHERE parent_object_id = OBJECT_ID(@p1) AND is_disabled = 0 ORDER BY name",
		sql.Named("p1", ref))
	if err != nil || len(names) == 0 {
		return "", "", err
	}
	for i, name := range names {
		names[i] = quoteIdent(name)
	}
	constraints := strings.Join(names, ", ")
	return "ALTER TABLE " + ref + " NOCHECK CONSTRAINT " + constraints,
		"ALTER TABLE " + ref + " WITH NOCHECK CHECK CONSTRAINT " + constraints, nil
}
