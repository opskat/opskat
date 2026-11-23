package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 转储：文件以 SET NAMES utf8mb4 开头，勾了「关闭外键检查」时接着关闭、结尾恢复；外键留在建表语句里（能在
// 会话里关闭外键检查，删除段也不先删外键）。定义都用服务器的原文（SHOW CREATE，DEFINER 保留；不勾「包含索引」
// 「包含外键」时去掉建表语句里对应的行），去掉对象所在库的限定（视图里的 `库`.`表` 等），文件才能还原进另一个名字
// 的库；过程、函数、触发器用 DELIMITER ;; 包起来；INSERT 不写生成列；视图给出它用到的视图与函数（见
// viewDependencies），编排据此把被依赖的先写。函数与过程不给依赖：服务器不记录过程体里用到的对象，建出时也不
// 检查它们，所以引用视图的函数写在视图之前（编排在没有依赖时的次序）也建得出。
var (
	_ sqldb.DumpDialect      = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

func (dialect) DumpSyntax() sqldb.DumpSyntax {
	return sqldb.DumpSyntax{
		Opening:                 []string{"SET NAMES utf8mb4;"},
		DisableForeignKeyChecks: []string{"SET FOREIGN_KEY_CHECKS = 0;"},
		EnableForeignKeyChecks:  []string{"SET FOREIGN_KEY_CHECKS = 1;"},
	}
}

func (dialect) DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	definition, referenced := stripDatabase(object.Definition, object.Database)
	switch object.Kind {
	case dbdriver.DumpTable:
		script.Definition = []string{sqldb.TerminateStatement(withoutTableLines(definition, object.Options))}
		if !object.WithData {
			return nil
		}
		columns, err := insertableColumns(ctx, conn, object)
		script.DataColumns = columns
		return err
	case dbdriver.ObjectView:
		script.Definition = []string{sqldb.TerminateStatement(definition)}
		used, err := viewDependencies(ctx, conn, object, referenced)
		script.Dependencies = used
		return err
	default:
		// 过程、函数、触发器体里的分号会被切成多条语句，要改分隔符。
		script.Definition = []string{"DELIMITER ;;", strings.TrimRight(definition, " \t\r\n") + "\n;;", "DELIMITER ;"}
		return nil
	}
}

// withoutTableLines 从 SHOW CREATE TABLE 的原文里去掉没勾的选项对应的行：不包含索引时去掉普通、FULLTEXT、
// SPATIAL 等索引的 KEY 行（主键与唯一约束留着），不包含外键时去掉 CONSTRAINT ... FOREIGN KEY 行。原文里列与
// 约束各占一行（以两个空格开头，以逗号分隔），去掉的是最后一行时前一行结尾的逗号也去掉。
func withoutTableLines(definition string, options dbdriver.DumpOptions) string {
	if options.Indexes && options.ForeignKeys {
		return definition
	}
	lines := strings.Split(definition, "\n")
	kept := make([]string, 0, len(lines))
	last := -1 // kept 里最后一个列或约束行的位置
	for _, line := range lines {
		body := strings.HasPrefix(line, "  ")
		if body && ((!options.Indexes && isIndexLine(line)) || (!options.ForeignKeys && isForeignKeyLine(line))) {
			continue
		}
		if !body && strings.HasPrefix(line, ")") && last >= 0 {
			kept[last] = strings.TrimSuffix(kept[last], ",")
		}
		if body {
			last = len(kept)
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// isIndexLine 报告建表语句里的一行是不是索引：KEY、FULLTEXT KEY、SPATIAL KEY 等（PRIMARY KEY 与 UNIQUE KEY 不算）。
func isIndexLine(line string) bool {
	words := strings.Fields(line)
	switch {
	case len(words) >= 2 && words[0] == "KEY":
		return true
	case len(words) >= 3 && words[1] == "KEY":
		return words[0] != "PRIMARY" && words[0] != "UNIQUE"
	}
	return false
}

// isForeignKeyLine 报告建表语句里的一行是不是外键：CONSTRAINT `名字` FOREIGN KEY (...)。
func isForeignKeyLine(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " "), "CONSTRAINT ")
	if !ok || !strings.HasPrefix(rest, "`") {
		return false
	}
	_, end := scanQuoted(rest, 0, '`')
	return strings.HasPrefix(rest[end:], " FOREIGN KEY ")
}

// insertableColumns 是 INSERT 写入的列：表的列去掉生成列（VIRTUAL / STORED GENERATED，不能写入）。
// EXTRA 里的 DEFAULT_GENERATED 是表达式默认值，不是生成列。
func insertableColumns(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject) ([]string, error) {
	columns, err := sqldb.QueryNames(ctx, conn,
		"SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? "+
			"AND EXTRA NOT LIKE '%VIRTUAL GENERATED%' AND EXTRA NOT LIKE '%STORED GENERATED%' ORDER BY ORDINAL_POSITION",
		object.Database, object.Name)
	if err != nil {
		return nil, sqldb.Wrap("columns", err)
	}
	return columns, nil
}

// errUnknownTable / errNoSuchTable 是查不存在的 information_schema 表时服务器的报错：5.7 与 MariaDB 没有
// information_schema.VIEW_TABLE_USAGE 与 VIEW_ROUTINE_USAGE，报 ER_UNKNOWN_TABLE；ER_NO_SUCH_TABLE 是一般的
// 「表不存在」。
const (
	errUnknownTable = 1109
	errNoSuchTable  = 1146
)

const (
	// viewTableUsageQuery 是视图用到的同库的表与视图（表不在视图、函数与过程那一段里，不影响次序）。
	viewTableUsageQuery = "SELECT TABLE_NAME FROM information_schema.VIEW_TABLE_USAGE WHERE VIEW_SCHEMA = ? AND TABLE_SCHEMA = VIEW_SCHEMA AND VIEW_NAME = ?"
	// viewRoutineUsageQuery 是视图调用的同库的函数（视图不能调用过程）。
	viewRoutineUsageQuery = "SELECT SPECIFIC_NAME FROM information_schema.VIEW_ROUTINE_USAGE WHERE TABLE_SCHEMA = ? AND SPECIFIC_SCHEMA = TABLE_SCHEMA AND TABLE_NAME = ?"
)

// viewDependencies 是视图用到的同库的视图与函数：8.0.13 起 information_schema.VIEW_TABLE_USAGE 与
// VIEW_ROUTINE_USAGE 有精确的记录；没有这两张表的服务器改看定义里写出的名字（referenced），每个名字都可能是
// 视图或函数（不是这一段里的对象的名字不影响次序）。
func viewDependencies(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, referenced []string) ([]dbdriver.DumpDependency, error) {
	var dependencies []dbdriver.DumpDependency
	for _, usage := range []struct{ kind, query string }{
		{dbdriver.ObjectView, viewTableUsageQuery}, {dbdriver.ObjectFunction, viewRoutineUsageQuery},
	} {
		used, err := sqldb.QueryNames(ctx, conn, usage.query, object.Database, object.Name)
		var mysqlErr *gomysql.MySQLError
		switch {
		case err == nil:
		case errors.As(err, &mysqlErr) && (mysqlErr.Number == errUnknownTable || mysqlErr.Number == errNoSuchTable):
			used = referenced
		default:
			return nil, sqldb.Wrap("view dependencies", err)
		}
		for _, name := range used {
			dependencies = append(dependencies, dbdriver.DumpDependency{Kind: usage.kind, Name: name})
		}
	}
	return dependencies, nil
}

// stripDatabase 去掉定义里写成 `database`.xxx 的库名限定（字符串与注释里的不动），并返回定义里出现的
// 全部反引号名字。服务器的 SHOW CREATE VIEW 在连接的默认库不是视图所在库时给每个对象都带上库名，过程体
// 里也可能是用户写的限定；不去掉，文件还原进别的库时它们仍指向源库。前面是「.」的名字是列名，不是库名。
func stripDatabase(definition, database string) (string, []string) {
	var out strings.Builder
	var names []string
	afterDot := false
	for i := 0; i < len(definition); {
		c := definition[i]
		switch {
		case c == '`':
			name, end := scanQuoted(definition, i, '`')
			names = append(names, name)
			if name == database && !afterDot && end < len(definition) && definition[end] == '.' {
				i, afterDot = end+1, false
				continue
			}
			out.WriteString(definition[i:end])
			i, afterDot = end, false
		case c == '\'' || c == '"':
			_, end := scanQuoted(definition, i, c)
			out.WriteString(definition[i:end])
			i, afterDot = end, false
		case strings.HasPrefix(definition[i:], "/*"):
			end := len(definition)
			if closing := strings.Index(definition[i+2:], "*/"); closing >= 0 {
				end = i + 2 + closing + 2
			}
			out.WriteString(definition[i:end])
			i = end
		case c == '#' || (strings.HasPrefix(definition[i:], "--") && (i+2 >= len(definition) || strings.ContainsRune(" \t\r\n", rune(definition[i+2])))):
			end := len(definition)
			if nl := strings.IndexByte(definition[i:], '\n'); nl >= 0 {
				end = i + nl + 1
			}
			out.WriteString(definition[i:end])
			i = end
		default:
			out.WriteByte(c)
			afterDot = c == '.'
			i++
		}
	}
	return out.String(), names
}

// scanQuoted 读出从 text[start]（引号 quote）开始的一段引号里的内容，连写两个引号是引号本身，字符串里
// 的反斜杠转义下一个字符（反引号名字里没有）；返回内容与引号之后的位置，没有结束的引号读到末尾。
func scanQuoted(text string, start int, quote byte) (string, int) {
	var b strings.Builder
	for i := start + 1; i < len(text); i++ {
		switch {
		case text[i] == '\\' && quote != '`' && i+1 < len(text):
			b.WriteByte(text[i])
			i++
			b.WriteByte(text[i])
		case text[i] != quote:
			b.WriteByte(text[i])
		case i+1 < len(text) && text[i+1] == quote:
			b.WriteByte(quote)
			i++
		default:
			return b.String(), i + 1
		}
	}
	return b.String(), len(text)
}
