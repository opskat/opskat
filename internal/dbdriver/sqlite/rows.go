package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 行语句：声明类型含 BLOB 的列（BLOB 亲和性）写成 X'…'；别的列（SQLite 的列可以不声明类型）里不是 UTF-8 文本
// 的值也写成 X'…'——它只能是 blob，写成字符串字面量文件就不是 UTF-8 了；按主键或全部列定位的 UPDATE 不另加限制。
var _ sqldb.RowWriter = dialect{}

var literals = sqldb.Literals{
	Formatted: func(_, text string) (string, bool) {
		if utf8.ValidString(text) {
			return "", false
		}
		return "X'" + hex.EncodeToString([]byte(text)) + "'", true
	},
	Binary: func(columnType string) (string, string, bool) {
		if strings.Contains(strings.ToUpper(columnType), "BLOB") {
			return "X'", "'", true
		}
		return "", "", false
	},
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) { return literals, nil }

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s;", tableRef, set, where)
}

// 表格编辑：一列都没填的新增行写 DEFAULT VALUES；没有主键时用 rowid 把全列匹配到的几行收敛成
// 物理上的一行（所以描述声明了 LocatesRowWithoutPrimaryKey）。
var _ sqldb.RowEditor = dialect{}

func (dialect) InsertRowSQL(tableRef, columns, values string, _ bool) string {
	if columns == "" {
		return fmt.Sprintf("INSERT INTO %s DEFAULT VALUES;", tableRef)
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tableRef, columns, values)
}

func (dialect) DeleteRowSQL(tableRef, where string) string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s;", tableRef, where)
}

func (dialect) ExactRow(tableRef, where string) (string, bool) {
	return fmt.Sprintf("rowid = (SELECT rowid FROM %s WHERE %s LIMIT 1)", tableRef, where), true
}
