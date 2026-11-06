package postgresql

import (
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 清空与新建数据库：TRUNCATE TABLE；CREATE DATABASE 的编码用 ENCODING，排序规则同时设 LC_COLLATE 与 LC_CTYPE，
// 留空则都不写、沿用模板库的。
var (
	_ sqldb.TableTruncator  = dialect{}
	_ sqldb.DatabaseCreator = dialect{}
)

func (dialect) TruncateTableSQL(tableRef string) string { return "TRUNCATE TABLE " + tableRef }

func (dialect) DatabaseOptionKeys() []string {
	return []string{dbdriver.OptionCharset, dbdriver.OptionCollation}
}

func (dialect) CreateDatabaseSQL(databaseRef string, options map[string]string) string {
	var with []string
	if charset := options[dbdriver.OptionCharset]; charset != "" {
		with = append(with, fmt.Sprintf("ENCODING = '%s'", charset))
	}
	if collation := options[dbdriver.OptionCollation]; collation != "" {
		with = append(with, fmt.Sprintf("LC_COLLATE = '%[1]s' LC_CTYPE = '%[1]s'", collation))
	}
	statement := "CREATE DATABASE " + databaseRef
	if len(with) > 0 {
		statement += " WITH " + strings.Join(with, " ")
	}
	return statement
}
