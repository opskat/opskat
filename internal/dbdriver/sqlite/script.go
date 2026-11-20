package sqlite

import "github.com/opskat/opskat/pkg/dbdriver/sqldb"

// 运行 SQL 文件：SQLite 的字符串用单引号，标识符可以用双引号、反引号或方括号；触发器体里的语句以
// 分号结束，整个 CREATE TRIGGER 到「; END;」才结束。
var _ sqldb.ScriptDialect = dialect{}

func (dialect) ScriptSyntax() sqldb.ScriptSyntax {
	return sqldb.ScriptSyntax{
		Quotes: []sqldb.ScriptQuote{
			{Open: '\'', Close: '\''}, {Open: '"', Close: '"'}, {Open: '`', Close: '`'}, {Open: '[', Close: ']'},
		},
		TriggerBodies: true,
	}
}
