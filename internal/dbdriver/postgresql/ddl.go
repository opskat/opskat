package postgresql

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 建表语句：PostgreSQL 不存原文，从 information_schema 重建。表名是 "schema.table"（裸名
// 按 public），列与主键都按表真正所在的 schema 查，语句里的表名也带这个 schema。
var _ sqldb.DDL = dialect{}

func (d dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	schema, tbl := splitSchemaTable(table)
	rows, err := conn.QueryContext(ctx, columnsQuery, schema, tbl)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var columns []sqldb.DDLColumn
	for rows.Next() {
		var name, dataType, nullable string
		var udtName, columnDefault sql.NullString
		if err := rows.Scan(&name, &dataType, &udtName, &nullable, &columnDefault); err != nil {
			return "", err
		}
		if dataType == "USER-DEFINED" && udtName.String != "" {
			dataType = udtName.String
		}
		columns = append(columns, sqldb.DDLColumn{
			Name: name, Type: dataType, Nullable: nullable == "YES", Default: columnDefault.String,
		})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", nil
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return "", err
	}
	return sqldb.BuildCreateTable(quoteIdent(schema)+"."+quoteIdent(tbl), columns, primaryKeys, quoteIdent), nil
}
