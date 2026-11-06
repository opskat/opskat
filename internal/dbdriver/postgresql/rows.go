package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 行语句：bytea 写成 '\x…'::bytea；按主键或全部列定位的 UPDATE 不另加限制。
var _ sqldb.RowWriter = dialect{}

var literals = sqldb.Literals{
	Binary: func(columnType string) (string, string, bool) {
		if strings.EqualFold(columnType, "bytea") {
			return `'\x`, `'::bytea`, true
		}
		return "", "", false
	},
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) { return literals, nil }

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s;", tableRef, set, where)
}

// 表格编辑：一列都没填的新增行写 DEFAULT VALUES；给 GENERATED ALWAYS 列写值要带 OVERRIDING
// SYSTEM VALUE；没有主键时用 (tableoid, ctid) 把全列匹配到的几行收敛成物理上的一行（所以描述声明了
// LocatesRowWithoutPrimaryKey）。ctid 只在单个分区 / 子表内唯一，查分区表或继承表的父表时不同
// 子表里会有相同的 ctid，带上 tableoid 才只改一行。
var _ sqldb.RowEditor = dialect{}

func (dialect) InsertRowSQL(tableRef, columns, values string, identity bool) string {
	switch {
	case columns == "":
		return fmt.Sprintf("INSERT INTO %s DEFAULT VALUES;", tableRef)
	case identity:
		return fmt.Sprintf("INSERT INTO %s (%s) OVERRIDING SYSTEM VALUE VALUES (%s);", tableRef, columns, values)
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", tableRef, columns, values)
}

func (dialect) DeleteRowSQL(tableRef, where string) string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s;", tableRef, where)
}

func (dialect) ExactRow(tableRef, where string) (string, bool) {
	return fmt.Sprintf("(tableoid, ctid) = (SELECT tableoid, ctid FROM %s WHERE %s LIMIT 1)", tableRef, where), true
}
