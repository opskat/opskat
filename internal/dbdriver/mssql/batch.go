package mssql

import "github.com/opskat/opskat/internal/dbdriver/sqldb"

// 整批执行时关闭外键检查：SQL Server 没有会话级的开关，停用的是批写入的那张表的约束；结束后
// WITH CHECK 重新启用，同时校验表里已有的数据。
var _ sqldb.ForeignKeyChecks = dialect{}

func (d dialect) ForeignKeyChecksSQL(database, table string) (string, string) {
	ref := d.TableRef(database, table)
	return "ALTER TABLE " + ref + " NOCHECK CONSTRAINT ALL", "ALTER TABLE " + ref + " WITH CHECK CHECK CONSTRAINT ALL"
}
