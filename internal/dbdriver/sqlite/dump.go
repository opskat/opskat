package sqlite

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 转储：文件开头关闭外键检查、结尾恢复（与 Navicat 一样），外键留在建表语句里。定义都用 sqlite_master
// 里的原文：表之后接着写它的索引（约束自动建的索引没有原文，随建表语句建出）；INSERT 不写生成列。
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
	if object.Kind != dbdriver.DumpTable {
		return nil
	}
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
