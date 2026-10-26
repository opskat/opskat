package mssql

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 浏览用 sys.* 与 INFORMATION_SCHEMA。库名写在连接串里，一条会话只连一个库；表与对象以
// "schema.name" 形式限定，裸名按 dbo。
var _ sqldb.Catalog = dialect{}

func (dialect) Descriptor() dbdriver.Descriptor { return descriptor }

func splitSchemaTable(table string) (string, string) {
	return sqldb.SplitSchemaTable(table, "dbo")
}

// ListDatabases 跳过系统库：database_id 1~4 是 master / tempdb / model / msdb。
func (dialect) ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error) {
	return sqldb.QueryNames(ctx, conn, "SELECT name FROM sys.databases WHERE database_id > 4 ORDER BY name")
}

func (dialect) ListTables(ctx context.Context, conn *sql.Conn, _ string) ([]string, error) {
	return sqldb.QueryNames(ctx, conn,
		"SELECT TABLE_SCHEMA + '.' + TABLE_NAME AS name FROM INFORMATION_SCHEMA.TABLES "+
			"WHERE TABLE_TYPE IN ('BASE TABLE', 'VIEW') ORDER BY TABLE_SCHEMA, TABLE_NAME")
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

func (dialect) indexes(ctx context.Context, conn *sql.Conn, schema, table string) ([]dbdriver.IndexMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT i.name, i.is_unique, c.name "+
			"FROM sys.indexes i "+
			"JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id "+
			"JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE s.name = @p1 AND o.name = @p2 AND i.name IS NOT NULL "+
			"ORDER BY i.name, ic.key_ordinal",
		sql.Named("p1", schema), sql.Named("p2", table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var indexes sqldb.IndexBuilder
	for rows.Next() {
		var name, col string
		var unique bool
		if err := rows.Scan(&name, &unique, &col); err != nil {
			return nil, err
		}
		indexes.Add(name, unique, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return indexes.Indexes(), nil
}

func (dialect) foreignKeys(ctx context.Context, conn *sql.Conn, schema, table string) ([]dbdriver.ForeignKeyMeta, error) {
	return sqldb.QueryForeignKeys(ctx, conn,
		"SELECT fk.name, pc.name, rt.name, rc.name "+
			"FROM sys.foreign_keys fk "+
			"JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id "+
			"JOIN sys.tables pt ON pt.object_id = fk.parent_object_id "+
			"JOIN sys.schemas s ON s.schema_id = pt.schema_id "+
			"JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id "+
			"JOIN sys.tables rt ON rt.object_id = fkc.referenced_object_id "+
			"JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id "+
			"WHERE s.name = @p1 AND pt.name = @p2 ORDER BY fk.name",
		sql.Named("p1", schema), sql.Named("p2", table))
}

func (dialect) ListObjects(ctx context.Context, conn *sql.Conn, _ string) (*dbdriver.DatabaseObjects, error) {
	out := &dbdriver.DatabaseObjects{}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT s.name + '.' + v.name FROM sys.views v JOIN sys.schemas s ON s.schema_id = v.schema_id ORDER BY 1",
		dbdriver.ObjectView, &out.Views); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT s.name + '.' + p.name FROM sys.procedures p JOIN sys.schemas s ON s.schema_id = p.schema_id ORDER BY 1",
		dbdriver.ObjectProcedure, &out.Procedures); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT s.name + '.' + o.name FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id WHERE o.type IN ('FN','IF','TF') ORDER BY 1",
		dbdriver.ObjectFunction, &out.Functions); err != nil {
		return nil, err
	}
	// DML 触发器经父对象带上 schema，ObjectSource 的 OBJECT_ID(name) 才能解析到正确的那个；
	// 数据库 / DDL 触发器没有 schema（parent_id 为 0），只给裸名。
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT CASE WHEN s.name IS NULL THEN tr.name ELSE s.name + '.' + tr.name END "+
			"FROM sys.triggers tr "+
			"LEFT JOIN sys.objects o ON o.object_id = tr.parent_id "+
			"LEFT JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE tr.is_ms_shipped = 0 ORDER BY 1",
		dbdriver.ObjectTrigger, &out.Triggers); err != nil {
		return nil, err
	}
	return out, nil
}

func (dialect) ObjectSource(ctx context.Context, conn *sql.Conn, _, _, name, _ string) (string, error) {
	var src sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))", sql.Named("p1", name)).Scan(&src); err != nil {
		return "", err
	}
	return src.String, nil
}
