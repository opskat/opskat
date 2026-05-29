package query_svc

import (
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// QuoteIdent 对单个 SQL 标识符按 driver 加引号。
// MySQL 反引号；PostgreSQL/SQLite 双引号；MSSQL 方括号。
// 标识符里同名转义字符按各方言规则成对转义。
//
// 行为与前端 frontend/src/lib/tableSql.ts:quoteIdent 等价,移到后端是为了
// OpenTable 等服务端拼装 SQL 时复用,不再依赖前端传 SQL 字符串。
func QuoteIdent(name string, driver asset_entity.DatabaseDriver) string {
	switch driver {
	case asset_entity.DriverPostgreSQL, asset_entity.DriverSQLite:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	case asset_entity.DriverMSSQL:
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default: // MySQL
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
}

// QuoteTableRef 把 db + table 拼成限定表引用。
// MySQL: `db`.`table`。
// PostgreSQL: 忽略 database 参数,table 可以是裸表名或 "schema.table" 形式。
// SQLite: database 表示 schema（main/temp/ATTACH 名称）,有值时保留 schema 限定。
// MSSQL: 与 PostgreSQL 一致,忽略 database 参数。连接已通过 DSN 的 database=
// 限定了 catalog,所以这里按 schema.table 加方括号即可(裸表名 → [table]、
// "dbo.users" → [dbo].[users])。不能拼成两段式 [db].[table]——T-SQL 会把它
// 解释为 schema=db、object=table,导致 "Invalid object name"。
func QuoteTableRef(database, table string, driver asset_entity.DatabaseDriver) string {
	switch driver {
	case asset_entity.DriverPostgreSQL, asset_entity.DriverMSSQL:
		return quoteQualified(table, driver)
	case asset_entity.DriverSQLite:
		if database != "" {
			return QuoteIdent(database, driver) + "." + QuoteIdent(table, driver)
		}
		return quoteQualified(table, driver)
	default: // MySQL
		return QuoteIdent(database, driver) + "." + QuoteIdent(table, driver)
	}
}

// SQLQuote 把字符串包成 SQL 字符串字面量,只做单引号转义('  -> ”)。
// 主要用于 information_schema 查询里需要字面量比较的字段(如 table_schema、
// table_name)。更安全的做法是参数化查询,但当前 information_schema 拼接
// 路径足够窄、SQL 直接落驱动层,这里用字面量转义即可。注意:只防 SQL 注入,
// 不做语义校验——调用方仍需保证传入的是预期的标识符。
func SQLQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteQualified(name string, driver asset_entity.DatabaseDriver) string {
	parts := strings.Split(name, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, QuoteIdent(p, driver))
	}
	return strings.Join(out, ".")
}
