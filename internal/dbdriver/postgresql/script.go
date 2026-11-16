package postgresql

import "github.com/opskat/opskat/pkg/dbdriver/sqldb"

// 运行 SQL 文件：按 psql 的认法切分。字符串用单引号（E'...' 里反斜杠转义）或 $$ / $tag$，标识符用
// 双引号；多行注释可以嵌套；BEGIN ATOMIC 函数体里的分号不结束语句。COPY ... FROM stdin 与反斜杠
// 元命令只有 psql 能执行，运行在那里停下。
var _ sqldb.ScriptDialect = dialect{}

func (dialect) ScriptSyntax() sqldb.ScriptSyntax {
	return sqldb.ScriptSyntax{
		Quotes:         []sqldb.ScriptQuote{{Open: '\'', Close: '\''}, {Open: '"', Close: '"'}},
		AtomicBodies:   true,
		NestedComments: true,
		DollarQuotes:   true,
		EscapeStrings:  true,
		PsqlCommands:   true,
	}
}
