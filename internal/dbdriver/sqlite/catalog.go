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

func (dialect) indexes(ctx context.Context, conn *sql.Conn, database, table string) ([]dbdriver.IndexMeta, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name, "unique" FROM `+qualify(database, "pragma_index_list(?)"), table) //nolint:gosec // schema is identifier-quoted by qualify.
	if err != nil {
		return nil, err
	}
	type idx struct {
		name   string
		unique bool
	}
	var list []idx
	for rows.Next() {
		var name string
		var uniq int
		if err := rows.Scan(&name, &uniq); err != nil {
			_ = rows.Close()
			return nil, err
		}
		list = append(list, idx{name: name, unique: uniq == 1})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	out := make([]dbdriver.IndexMeta, 0, len(list))
	for _, it := range list {
		colRows, err := conn.QueryContext(ctx, "SELECT name FROM pragma_index_info(?)", it.name)
		if err != nil {
			return nil, err
		}
		var cols []string
		for colRows.Next() {
			var c sql.NullString
			if err := colRows.Scan(&c); err != nil {
				_ = colRows.Close()
				return nil, err
			}
			if c.Valid {
				cols = append(cols, c.String)
			}
		}
		if err := colRows.Err(); err != nil {
			_ = colRows.Close()
			return nil, err
		}
		_ = colRows.Close()
		out = append(out, dbdriver.IndexMeta{Name: it.name, Columns: cols, Unique: it.unique})
	}
	return out, nil
}

func (dialect) foreignKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]dbdriver.ForeignKeyMeta, error) {
	rows, err := conn.QueryContext(ctx, `SELECT "table", "from", "to" FROM `+qualify(database, "pragma_foreign_key_list(?)"), table) //nolint:gosec // schema is identifier-quoted by qualify.
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]dbdriver.ForeignKeyMeta, 0)
	for rows.Next() {
		var refTable, from, to string
		if err := rows.Scan(&refTable, &from, &to); err != nil {
			return nil, err
		}
		out = append(out, dbdriver.ForeignKeyMeta{Column: from, ReferencedTable: refTable, ReferencedColumn: to})
	}
	return out, rows.Err()
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
