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

// 转储：文件以 SET NAMES utf8mb4 与关闭外键检查开头、结尾恢复，外键留在建表语句里。定义都用服务器的原文
// （SHOW CREATE，DEFINER 保留），去掉对象所在库的限定（视图里的 `库`.`表` 等），文件才能还原进另一个名字
// 的库；过程、函数、触发器用 DELIMITER ;; 包起来；INSERT 不写生成列；视图给出它用到的对象，编排据此把被
// 依赖的视图先写。
var (
	_ sqldb.DumpDialect      = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

func (dialect) DumpSyntax() sqldb.DumpSyntax {
	return sqldb.DumpSyntax{
		Opening: []string{"SET NAMES utf8mb4;", "SET FOREIGN_KEY_CHECKS = 0;"},
		Closing: []string{"SET FOREIGN_KEY_CHECKS = 1;"},
	}
}

func (dialect) DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	definition, referenced := stripDatabase(object.Definition, object.Database)
	switch object.Kind {
	case dbdriver.DumpTable:
		script.Definition = []string{sqldb.TerminateStatement(definition)}
		if !object.WithData {
			return nil
		}
		columns, err := insertableColumns(ctx, conn, object)
		script.DataColumns = columns
		return err
	case dbdriver.ObjectView:
		script.Definition = []string{sqldb.TerminateStatement(definition)}
		used, err := viewDependencies(ctx, conn, object, referenced)
		script.DependsOn = used
		return err
	default:
		// 过程、函数、触发器体里的分号会被切成多条语句，要改分隔符。
		script.Definition = []string{"DELIMITER ;;", strings.TrimRight(definition, " \t\r\n") + "\n;;", "DELIMITER ;"}
		return nil
	}
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

// errNoSuchTable 是 MySQL 的 ER_NO_SUCH_TABLE：5.7 与 MariaDB 没有 information_schema.VIEW_TABLE_USAGE。
const errNoSuchTable = 1146

// viewDependencies 是视图用到的同库对象：8.0.13 起 information_schema.VIEW_TABLE_USAGE 有精确的记录；没有
// 这张表的服务器改看定义里写出的名字（referenced），其中不是这批视图的名字不影响次序。
func viewDependencies(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, referenced []string) ([]string, error) {
	used, err := sqldb.QueryNames(ctx, conn,
		"SELECT TABLE_NAME FROM information_schema.VIEW_TABLE_USAGE WHERE VIEW_SCHEMA = ? AND TABLE_SCHEMA = VIEW_SCHEMA AND VIEW_NAME = ?",
		object.Database, object.Name)
	var mysqlErr *gomysql.MySQLError
	switch {
	case err == nil:
		return used, nil
	case errors.As(err, &mysqlErr) && mysqlErr.Number == errNoSuchTable:
		return referenced, nil
	}
	return nil, sqldb.Wrap("view dependencies", err)
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
