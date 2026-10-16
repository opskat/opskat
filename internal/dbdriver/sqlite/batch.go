package sqlite

import "github.com/opskat/opskat/internal/dbdriver/sqldb"

// 整批执行时关闭外键检查：PRAGMA foreign_keys 作用于这条连接，在事务里切换不起作用——sqldb
// 在事务之外执行它。
var _ sqldb.ForeignKeyChecks = dialect{}

func (dialect) ForeignKeyChecksSQL(_, _ string) (string, string) {
	return "PRAGMA foreign_keys = OFF", "PRAGMA foreign_keys = ON"
}
