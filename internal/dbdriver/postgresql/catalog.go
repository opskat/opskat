package postgresql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// dialect 是 PostgreSQL 的方言：分页用共用的 LIMIT / OFFSET，浏览用 pg_catalog 与
// information_schema。库名写在连接串里，一条会话只连一个库；表与对象以 "schema.name" 形式
// 限定，裸名按 public。
type dialect struct{ sqldb.LimitOffset }

var _ sqldb.Catalog = dialect{}

func (dialect) Descriptor() dbdriver.Descriptor { return descriptor }

const systemSchemas = "('pg_catalog','information_schema')"

func splitSchemaTable(table string) (string, string) {
	return sqldb.SplitSchemaTable(table, "public")
}

func (dialect) ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error) {
	return sqldb.QueryNames(ctx, conn, "SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname")
}

func (dialect) ListTables(ctx context.Context, conn *sql.Conn, _ string) ([]string, error) {
	return sqldb.QueryNames(ctx, conn,
		"SELECT table_schema || '.' || table_name AS name FROM information_schema.tables "+
			"WHERE table_schema NOT IN "+systemSchemas+" "+
			"AND table_type IN ('BASE TABLE', 'VIEW') ORDER BY table_schema, table_name")
}

func (d dialect) TableMetadata(ctx context.Context, conn *sql.Conn, database, table string) (*dbdriver.TableMetadata, error) {
	schema, tbl := splitSchemaTable(table)
	columns, err := d.TableColumns(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("columns", err)
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("primary keys", err)
	}
	indexes, err := d.indexes(ctx, conn, schema, tbl)
	if err != nil {
		return nil, sqldb.Wrap("indexes", err)
	}
	foreignKeys, err := d.foreignKeys(ctx, conn, schema, tbl)
	if err != nil {
		return nil, sqldb.Wrap("foreign keys", err)
	}
	return sqldb.AssembleMetadata(sqldb.ColumnMetas(columns), primaryKeys, indexes, foreignKeys), nil
}

// indexes 读表的索引（同改表模型，见 readIndexes）：键列按它在索引键里的位置，表达式键是表达式原文，INCLUDE
// 列单列在 Include 里。
func (dialect) indexes(ctx context.Context, conn *sql.Conn, schema, table string) ([]dbdriver.IndexMeta, error) {
	read, err := readIndexes(ctx, conn, schema, table)
	if err != nil {
		return nil, err
	}
	indexes := make([]dbdriver.IndexMeta, len(read))
	for i, index := range read {
		indexes[i] = index.meta()
	}
	return indexes, nil
}

// foreignKeysSQL 读外键的列映射：按 pg_constraint 的 conkey / confkey 逐位对应（多列外键一列一行），带上 ON UPDATE /
// ON DELETE 的动作（最后两列，见 sqldb.QueryForeignKeys）。被引用表只给表名，同改动前。
const foreignKeysSQL = "SELECT con.conname AS constraint_name, a.attname AS column_name, rt.relname AS referenced_table, " +
	"ra.attname AS referenced_column, " + foreignKeyActionUpdate + " AS on_update, " + foreignKeyActionDelete + " AS on_delete " +
	"FROM pg_constraint con " +
	"JOIN pg_class t ON t.oid = con.conrelid " +
	"JOIN pg_namespace n ON n.oid = t.relnamespace " +
	"JOIN pg_class rt ON rt.oid = con.confrelid " +
	"CROSS JOIN LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS k(attnum, refattnum, pos) " +
	"JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum " +
	"JOIN pg_attribute ra ON ra.attrelid = con.confrelid AND ra.attnum = k.refattnum " +
	"WHERE con.contype = 'f' AND n.nspname = $1 AND t.relname = $2 " +
	"ORDER BY con.conname, k.pos"

// confupdtype / confdeltype 的动作码换成动作名（a 是 NO ACTION）。
const (
	foreignKeyActionUpdate = "CASE con.confupdtype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' " +
		"WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END"
	foreignKeyActionDelete = "CASE con.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' " +
		"WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END"
)

func (dialect) foreignKeys(ctx context.Context, conn *sql.Conn, schema, table string) ([]dbdriver.ForeignKeyMeta, error) {
	return sqldb.QueryForeignKeys(ctx, conn, foreignKeysSQL, schema, table)
}

func (dialect) ListObjects(ctx context.Context, conn *sql.Conn, _ string) (*dbdriver.DatabaseObjects, error) {
	out := &dbdriver.DatabaseObjects{}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT table_schema || '.' || table_name FROM information_schema.views WHERE table_schema NOT IN "+systemSchemas+" ORDER BY 1",
		dbdriver.ObjectView, &out.Views); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT routine_schema || '.' || routine_name FROM information_schema.routines WHERE routine_schema NOT IN "+systemSchemas+" AND routine_type = 'PROCEDURE' ORDER BY 1",
		dbdriver.ObjectProcedure, &out.Procedures); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT routine_schema || '.' || routine_name FROM information_schema.routines WHERE routine_schema NOT IN "+systemSchemas+" AND routine_type = 'FUNCTION' ORDER BY 1",
		dbdriver.ObjectFunction, &out.Functions); err != nil {
		return nil, err
	}
	// 触发器同时带上它的表：PostgreSQL 的触发器名在表内才唯一，查源码要靠所属的表。
	trigRows, err := conn.QueryContext(ctx,
		"SELECT DISTINCT trigger_schema, trigger_name, event_object_table "+
			"FROM information_schema.triggers "+
			"WHERE trigger_schema NOT IN "+systemSchemas+" "+
			"ORDER BY trigger_schema, trigger_name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = trigRows.Close() }()
	for trigRows.Next() {
		var schema, name, tbl string
		if err := trigRows.Scan(&schema, &name, &tbl); err != nil {
			return nil, err
		}
		out.Triggers = append(out.Triggers, dbdriver.ObjectMeta{Name: schema + "." + name, Type: dbdriver.ObjectTrigger, Table: tbl})
	}
	if err := trigRows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (dialect) ObjectSource(ctx context.Context, conn *sql.Conn, _, objType, name, table string) (string, error) {
	schema, obj := splitSchemaTable(name)
	var src sql.NullString
	var err error
	switch strings.ToLower(objType) {
	case dbdriver.ObjectView:
		err = conn.QueryRowContext(ctx, viewDefQuery, schema, obj).Scan(&src)
	case dbdriver.ObjectTrigger:
		// 触发器在 pg_trigger 而不是 pg_proc 里，由（schema、表、名字）确定；obj 是触发器名，
		// table 是它所属的表。
		err = conn.QueryRowContext(ctx,
			"SELECT pg_get_triggerdef(tg.oid, true) "+
				"FROM pg_trigger tg "+
				"JOIN pg_class c ON c.oid = tg.tgrelid "+
				"JOIN pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2 AND tg.tgname = $3 AND NOT tg.tgisinternal "+
				"LIMIT 1", schema, table, obj).Scan(&src)
	default: // 存储过程 / 函数
		err = conn.QueryRowContext(ctx,
			"SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace "+
				"WHERE n.nspname = $1 AND p.proname = $2 LIMIT 1", schema, obj).Scan(&src)
	}
	if err != nil {
		return "", err
	}
	return src.String, nil
}
