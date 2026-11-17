package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 转储：勾了「关闭外键检查」时文件开头关闭、结尾恢复（与 Navicat 一样），外键留在建表语句里（能在会话里关闭
// 外键检查，删除段也不先删外键；不勾「包含外键」时从原文里去掉）。定义都用 sqlite_master 里的原文：勾了「包含
// 索引」时表之后接着写它的索引（约束自动建的索引没有原文，随建表语句建出）；INSERT 不写生成列；视图给出
// 它用到的其他视图，编排据此把被依赖的先写；触发器用默认写法，编排把它们写在所有数据之后。
var (
	_ sqldb.DumpDialect      = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

func (dialect) DumpSyntax() sqldb.DumpSyntax {
	return sqldb.DumpSyntax{
		DisableForeignKeyChecks: []string{"PRAGMA foreign_keys = false;"},
		EnableForeignKeyChecks:  []string{"PRAGMA foreign_keys = true;"},
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

// dumpTable 不包含外键时从建表语句里去掉外键，包含索引时在建表语句之后接着写表的索引；结构和数据时 INSERT
// 不写生成列。
func dumpTable(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	if !object.Options.ForeignKeys {
		script.Definition = []string{sqldb.TerminateStatement(withoutForeignKeys(object.Definition))}
	}
	if object.Options.Indexes {
		if err := dumpIndexes(ctx, conn, object, script); err != nil {
			return err
		}
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

// dumpIndexes 在建表语句之后接着写表的索引（sqlite_master 里的原文）。
func dumpIndexes(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	indexes, err := sqldb.QueryNames(ctx, conn,
		"SELECT sql FROM "+qualify(object.Database, "sqlite_master")+" WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL ORDER BY name",
		object.Name)
	if err != nil {
		return sqldb.Wrap("indexes", err)
	}
	for _, index := range indexes {
		script.Definition = append(script.Definition, sqldb.TerminateStatement(index))
	}
	return nil
}

// withoutForeignKeys 去掉 sqlite_master 里建表语句原文中的外键（不勾「包含外键」时）：表约束
// [CONSTRAINT 名字] FOREIGN KEY (...) REFERENCES ... 连同它前面的逗号，列约束 [CONSTRAINT 名字] REFERENCES ...
// 连同它前面的空白；其余（字符串、注释、名字里的同名词）一字不动。列与约束在第一组括号里，没有这组括号的
// （CREATE TABLE ... AS SELECT、虚表的模块参数）原样返回。
func withoutForeignKeys(definition string) string {
	tokens := sqlTokens(definition, 0, len(definition))
	body := slices.IndexFunc(tokens, func(t sqlToken) bool { return t.text[0] == '(' })
	if body < 0 || slices.ContainsFunc(tokens[:body], func(t sqlToken) bool { return t.is("AS") || t.is("USING") }) {
		return definition
	}
	inner := sqlTokens(definition, tokens[body].start+1, tokens[body].end-1)
	var cuts [][2]int
	for start := 0; start < len(inner); {
		end := start
		for end < len(inner) && inner[end].text != "," {
			end++
		}
		item := inner[start:end]
		switch {
		case len(item) == 0:
		case item[0].is("FOREIGN") || (len(item) > 2 && item[0].is("CONSTRAINT") && item[2].is("FOREIGN")):
			if start > 0 { // 前面的逗号一起去掉；第一项是列，不会是表约束
				cuts = append(cuts, [2]int{inner[start-1].start, item[len(item)-1].end})
			}
		default:
			for i := 1; i < len(item); i++ {
				if !item[i].is("REFERENCES") {
					continue
				}
				first := i
				if i >= 3 && item[i-2].is("CONSTRAINT") {
					first = i - 2
				}
				last := foreignKeyClauseEnd(item, i)
				cuts = append(cuts, [2]int{item[first-1].end, item[last].end})
				i = last
			}
		}
		start = end + 1
	}
	var out strings.Builder
	from := 0
	for _, cut := range cuts {
		out.WriteString(definition[from:cut[0]])
		from = cut[1]
	}
	out.WriteString(definition[from:])
	return out.String()
}

// foreignKeyClauseEnd 是从 tokens[references]（REFERENCES）开始的外键子句的最后一个记号：表名、可选的列、
// 以及任意个 ON DELETE / ON UPDATE 动作、MATCH 名字、[NOT] DEFERRABLE [INITIALLY DEFERRED | IMMEDIATE]。
func foreignKeyClauseEnd(tokens []sqlToken, references int) int {
	last := len(tokens) - 1
	i := min(references+1, last) // 被引用的表
	if i < last && tokens[i+1].text[0] == '(' {
		i++
	}
	for i < last {
		next := tokens[i+1]
		switch {
		case next.is("ON") && i+3 <= last:
			i += 3 // ON DELETE|UPDATE 动作
			if (tokens[i].is("SET") || tokens[i].is("NO")) && i < last {
				i++ // SET NULL、SET DEFAULT、NO ACTION
			}
		case next.is("MATCH") && i+2 <= last:
			i += 2
		case next.is("DEFERRABLE") || (next.is("NOT") && i+2 <= last && tokens[i+2].is("DEFERRABLE")):
			if next.is("NOT") {
				i++
			}
			i++
			if i+2 <= last && tokens[i+1].is("INITIALLY") {
				i += 2
			}
		default:
			return i
		}
	}
	return i
}

// sqlToken 是一段 SQL 里的一个记号：裸的词、引号里的名字或字符串、一组括号（含里面的全部）或一个标点，
// start / end 是它在原文里的位置。
type sqlToken struct {
	text       string
	start, end int
}

// is 报告记号是不是关键字 word（不分大小写）。
func (t sqlToken) is(word string) bool { return strings.EqualFold(t.text, word) }

// sqlTokens 是 text[from:to] 里的记号，跳过空白与注释。
func sqlTokens(text string, from, to int) []sqlToken {
	text = text[:to]
	var tokens []sqlToken
	for i := from; ; {
		start, end := nextToken(text, i)
		if start == len(text) {
			return tokens
		}
		tokens = append(tokens, sqlToken{text: text[start:end], start: start, end: end})
		i = end
	}
}

// nextToken 是 text[i:] 里下一个记号的起止（跳过空白与注释）；没有了时 start 为 len(text)。
func nextToken(text string, i int) (int, int) {
	for i < len(text) {
		switch c := text[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f':
			i++
		case strings.HasPrefix(text[i:], "--"):
			if end := strings.IndexByte(text[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(text)
			}
		case strings.HasPrefix(text[i:], "/*"):
			if end := strings.Index(text[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				i = len(text)
			}
		default:
			return i, tokenEnd(text, i)
		}
	}
	return len(text), len(text)
}

// tokenEnd 是从 text[i] 开始的记号之后的位置；一组括号到与它配对的右括号之后（里面的引号与注释不算括号）。
func tokenEnd(text string, i int) int {
	switch c := text[i]; {
	case c == '\'' || c == '"' || c == '`':
		_, end := quotedText(text, i, c)
		return end
	case c == '[':
		if end := strings.IndexByte(text[i:], ']'); end >= 0 {
			return i + end + 1
		}
		return len(text)
	case c == '(':
		for j := i + 1; ; {
			start, end := nextToken(text, j)
			if start == len(text) || text[start] == ')' {
				return end
			}
			j = end
		}
	case identifierByte(c):
		j := i
		for j < len(text) && identifierByte(text[j]) {
			j++
		}
		return j
	}
	return i + 1
}
