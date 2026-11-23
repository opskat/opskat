package mysql

import "github.com/opskat/opskat/pkg/dbdriver/sqldb"

// 运行 SQL 文件：按 mysql 客户端的认法切分。字符串用单引号或双引号，里面的反斜杠转义下一个字符；
// 标识符用反引号；# 与「-- 」到行尾是注释，/*! ... */ 版本注释与 /*+ ... */ 提示是语句的一部分；
// DELIMITER 一行换掉之后语句的结束符（过程、触发器体里有分号）。
var _ sqldb.ScriptDialect = dialect{}

func (dialect) ScriptSyntax() sqldb.ScriptSyntax {
	return sqldb.ScriptSyntax{
		Quotes: []sqldb.ScriptQuote{
			{Open: '\'', Close: '\'', Backslash: true}, {Open: '"', Close: '"', Backslash: true}, {Open: '`', Close: '`'},
		},
		HashComments:       true,
		SpacedDashComments: true,
		ExecutableComments: true,
		Delimiter:          true,
	}
}
