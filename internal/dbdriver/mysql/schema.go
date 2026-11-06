package mysql

import (
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 清空与新建数据库：TRUNCATE TABLE；CREATE DATABASE 带 CHARACTER SET / COLLATE。
var (
	_ sqldb.TableTruncator  = dialect{}
	_ sqldb.DatabaseCreator = dialect{}
)

func (dialect) TruncateTableSQL(tableRef string) string { return "TRUNCATE TABLE " + tableRef }

func (dialect) DatabaseOptionKeys() []string {
	return []string{dbdriver.OptionCharset, dbdriver.OptionCollation}
}

func (dialect) CreateDatabaseSQL(databaseRef string, options map[string]string) string {
	parts := []string{"CREATE DATABASE " + databaseRef}
	if charset := options[dbdriver.OptionCharset]; charset != "" {
		parts = append(parts, "CHARACTER SET "+charset)
	}
	if collation := options[dbdriver.OptionCollation]; collation != "" {
		parts = append(parts, "COLLATE "+collation)
	}
	return strings.Join(parts, " ")
}
