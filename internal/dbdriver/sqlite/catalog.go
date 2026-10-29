package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// dialect 是 SQLite 的方言：分页用共用的 LIMIT / OFFSET，浏览用 sqlite_master 与 pragma。
// 库名是 schema 名（main / temp / ATTACH 的名字），空表示 main。
type dialect struct{ sqldb.LimitOffset }

var _ sqldb.Catalog = dialect{}

func (dialect) Descriptor() dbdriver.Descriptor { return descriptor }

// quoteIdent 给标识符加双引号，内部的双引号成对转义。
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// qualify 给 schema 里的对象名加限定；database 为空时不限定（SQLite 按 main、temp 的次序解析）。
func qualify(database, object string) string {
	if database == "" {
		return object
	}
	return quoteIdent(database) + "." + object
}

func (dialect) ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error) {
	names, err := sqldb.QueryNames(ctx, conn, "SELECT name FROM pragma_database_list ORDER BY seq")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return []string{"main"}, nil
	}
	return names, nil
}

func (dialect) ListTables(ctx context.Context, conn *sql.Conn, database string) ([]string, error) {
	schema := "main"
	if database != "" {
		schema = quoteIdent(database)
	}
	return sqldb.QueryNames(ctx, conn,
		"SELECT name FROM "+schema+".sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name")
}

func (d dialect) TableMetadata(ctx context.Context, conn *sql.Conn, database, table string) (*dbdriver.TableMetadata, error) {
	columns, err := d.TableColumns(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("columns", err)
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("primary keys", err)
	}
	indexes, err := d.indexes(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("indexes", err)
	}
	foreignKeys, err := d.foreignKeys(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("foreign keys", err)
	}
	return sqldb.AssembleMetadata(sqldb.ColumnMetas(columns), primaryKeys, indexes, foreignKeys), nil
}

// indexes 是对象浏览里的索引，按 pragma_index_list 的次序（含约束自动建的）；表达式键以表达式原文作列。
func (dialect) indexes(ctx context.Context, conn *sql.Conn, database, table string) ([]dbdriver.IndexMeta, error) {
	indexes, err := readIndexes(ctx, conn, database, table)
	if err != nil {
		return nil, err
	}
	out := make([]dbdriver.IndexMeta, len(indexes))
	for i, index := range indexes {
		out[i] = index.meta()
	}
	return out, nil
}

// foreignKeys 读外键与 ON UPDATE / ON DELETE 的动作。SQLite 不保存外键约束的名字（pragma 里没有），名字为空。
func (dialect) foreignKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]dbdriver.ForeignKeyMeta, error) {
	return sqldb.QueryForeignKeys(ctx, conn,
		`SELECT '' AS name, "from", "table", "to", on_update, on_delete FROM `+qualify(database, "pragma_foreign_key_list(?)"), table)
}

// ListObjects：SQLite 只有视图与触发器，没有存储过程和函数。
func (dialect) ListObjects(ctx context.Context, conn *sql.Conn, database string) (*dbdriver.DatabaseObjects, error) {
	rows, err := conn.QueryContext(ctx, "SELECT type, name FROM "+qualify(database, "sqlite_master")+" WHERE type IN ('view','trigger') ORDER BY name") //nolint:gosec // schema is identifier-quoted by qualify.
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &dbdriver.DatabaseObjects{}
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			return nil, err
		}
		if typ == "view" {
			out.Views = append(out.Views, dbdriver.ObjectMeta{Name: name, Type: dbdriver.ObjectView})
		} else {
			out.Triggers = append(out.Triggers, dbdriver.ObjectMeta{Name: name, Type: dbdriver.ObjectTrigger})
		}
	}
	return out, rows.Err()
}

// ObjectSource 读 sqlite_master.sql；视图与触发器的名字在库内各自唯一。
func (dialect) ObjectSource(ctx context.Context, conn *sql.Conn, database, _, name, _ string) (string, error) {
	var src sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT sql FROM "+qualify(database, "sqlite_master")+" WHERE name = ?", name).Scan(&src)
	if err != nil {
		return "", err
	}
	return src.String, nil
}
