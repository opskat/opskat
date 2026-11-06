package mssql

import (
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 清空与新建数据库：TRUNCATE TABLE；SQL Server 没有 CHARACTER SET，只有 COLLATE。
var (
	_ sqldb.TableTruncator  = dialect{}
	_ sqldb.DatabaseCreator = dialect{}
)

func (dialect) TruncateTableSQL(tableRef string) string { return "TRUNCATE TABLE " + tableRef }

func (dialect) DatabaseOptionKeys() []string { return []string{dbdriver.OptionCollation} }

func (dialect) CreateDatabaseSQL(databaseRef string, options map[string]string) string {
	statement := "CREATE DATABASE " + databaseRef
	if collation := options[dbdriver.OptionCollation]; collation != "" {
		statement += " COLLATE " + collation
	}
	return statement
}
