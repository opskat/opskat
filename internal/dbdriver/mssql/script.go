package mssql

import "github.com/opskat/opskat/pkg/dbdriver/sqldb"

// 运行 SQL 文件：按 sqlcmd / SSMS 的认法，单独一行的 GO 分开一个个批，每批作为一次执行交给数据库
// （批里的分号不切分，CREATE PROCEDURE 这类必须独占一批的语句才能执行）。字符串用单引号，标识符用
// 双引号或方括号；多行注释可以嵌套。
var _ sqldb.ScriptDialect = dialect{}

func (dialect) ScriptSyntax() sqldb.ScriptSyntax {
	return sqldb.ScriptSyntax{
		Quotes:         []sqldb.ScriptQuote{{Open: '\'', Close: '\''}, {Open: '"', Close: '"'}, {Open: '[', Close: ']'}},
		NestedComments: true,
		BatchSeparator: "GO",
	}
}
