package mysql

import "github.com/opskat/opskat/internal/dbdriver/sqldb"

// 整批执行时关闭外键检查：FOREIGN_KEY_CHECKS 是会话级的，只作用于批的那条连接。
var _ sqldb.ForeignKeyChecks = dialect{}

func (dialect) ForeignKeyChecksSQL(_, _ string) (string, string) {
	return "SET FOREIGN_KEY_CHECKS = 0", "SET FOREIGN_KEY_CHECKS = 1"
}
