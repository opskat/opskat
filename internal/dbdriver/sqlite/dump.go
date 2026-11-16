package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 转储：文件开头关闭外键检查、结尾恢复（与 Navicat 一样），外键留在建表语句里。定义都用 sqlite_master
// 里的原文：表之后接着写它的索引（约束自动建的索引没有原文，随建表语句建出）；INSERT 不写生成列；视图给出
// 它用到的其他视图，编排据此把被依赖的先写；触发器用默认写法，编排把它们写在所有数据之后。
var (
	_ sqldb.DumpDialect      = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

func (dialect) DumpSyntax() sqldb.DumpSyntax {
	return sqldb.DumpSyntax{
		Opening: []string{"PRAGMA foreign_keys = false;"},
		Closing: []string{"PRAGMA foreign_keys = true;"},
	}
}

func (dialect) DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	switch object.Kind {
	case dbdriver.DumpTable:
		return dumpTable(ctx, conn, object, script)
	case dbdriver.ObjectView:
		return dumpView(ctx, conn, object, script)
	}
	return nil
}

// dumpView 给出视图用到的其他视图（DependsOn）：SQLite 不记录视图之间的依赖，就看定义里的标识符（字符串
// 与注释除外）有哪些是库里别的视图的名字。SQLite 的标识符不分 ASCII 大小写。
func dumpView(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	views, err := sqldb.QueryNames(ctx, conn,
		"SELECT name FROM "+qualify(object.Database, "sqlite_master")+" WHERE type = 'view' AND name <> ? ORDER BY name", object.Name)
	if err != nil {
		return sqldb.Wrap("views", err)
	}
	used := make(map[string]bool)
	for _, name := range identifiers(object.Definition) {
		used[strings.ToLower(name)] = true
	}
	for _, view := range views {
		if used[strings.ToLower(view)] {
			script.DependsOn = append(script.DependsOn, view)
		}
	}
	return nil
}

// identifiers 给出一段 SQL 里的标识符：裸的词与加了双引号、反引号、方括号的名字（去掉引号），跳过字符串、
// 数字与注释。
func identifiers(text string) []string {
	var names []string
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\'':
			_, i = quotedText(text, i, '\'')
		case c == '"' || c == '`':
			var name string
			name, i = quotedText(text, i, c)
			names = append(names, name)
		case c == '[':
			end := strings.IndexByte(text[i+1:], ']')
			if end < 0 {
				return append(names, text[i+1:])
			}
			names = append(names, text[i+1:i+1+end])
			i += end + 2
		case strings.HasPrefix(text[i:], "--"):
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				return names
			}
			i += end + 1
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return names
			}
			i += end + 4
		case identifierByte(c):
			start := i
			for i < len(text) && identifierByte(text[i]) {
				i++
			}
			if c < '0' || c > '9' {
				names = append(names, text[start:i])
			}
		default:
			i++
		}
	}
	return names
}

// quotedText 读出从 text[start]（引号 quote）开始的一段引号里的内容，连写两个引号是引号本身；返回内容与
// 引号之后的位置。
func quotedText(text string, start int, quote byte) (string, int) {
	var b strings.Builder
	for i := start + 1; i < len(text); i++ {
		if text[i] != quote {
			b.WriteByte(text[i])
			continue
		}
		if i+1 < len(text) && text[i+1] == quote {
			b.WriteByte(quote)
			i++
			continue
		}
		return b.String(), i + 1
	}
	return b.String(), len(text)
}

// identifierByte 报告 c 能否出现在裸的标识符里（UTF-8 的多字节字符都算）。
func identifierByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// dumpTable 在建表语句之后接着写表的索引；结构和数据时 INSERT 不写生成列。
func dumpTable(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	indexes, err := sqldb.QueryNames(ctx, conn,
		"SELECT sql FROM "+qualify(object.Database, "sqlite_master")+" WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL ORDER BY name",
		object.Name)
	if err != nil {
		return sqldb.Wrap("indexes", err)
	}
	for _, index := range indexes {
		script.Definition = append(script.Definition, sqldb.TerminateStatement(index))
	}
	if !object.WithData {
		return nil
	}
	// hidden：0 是普通列，1 是虚表的隐藏列（不在 SELECT * 里），2 / 3 是虚拟 / 存储的生成列（写不进去）。
	columns, err := sqldb.QueryNames(ctx, conn,
		"SELECT name FROM "+qualify(object.Database, "pragma_table_xinfo(?)")+" WHERE hidden = 0 ORDER BY cid", object.Name)
	if err != nil {
		return sqldb.Wrap("columns", err)
	}
	script.DataColumns = columns
	return nil
}
