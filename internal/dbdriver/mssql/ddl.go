package mssql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 建表语句：SQL Server 不存原文，从 INFORMATION_SCHEMA 重建。表名是 "schema.table"（裸名
// 按 dbo 查询），语句里的表名保持收到的写法。
var _ sqldb.DDL = dialect{}

func (d dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	schema, tbl := splitSchemaTable(table)
	rows, err := conn.QueryContext(ctx,
		"SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, IS_NULLABLE, COLUMN_DEFAULT "+
			"FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 ORDER BY ORDINAL_POSITION",
		sql.Named("p1", schema), sql.Named("p2", tbl))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var columns []sqldb.DDLColumn
	for rows.Next() {
		var name, dataType, nullable string
		var maxLength sql.NullInt64
		var columnDefault sql.NullString
		if err := rows.Scan(&name, &dataType, &maxLength, &nullable, &columnDefault); err != nil {
			return "", err
		}
		switch {
		case maxLength.Int64 == -1 && maxLength.Valid:
			dataType += "(max)"
		case maxLength.Int64 > 0:
			dataType += fmt.Sprintf("(%d)", maxLength.Int64)
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
	return sqldb.BuildCreateTable(d.TableRef(database, table), columns, primaryKeys, quoteIdent), nil
}
