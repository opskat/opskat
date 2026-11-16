package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 建表语句：SQL Server 不存原文，从系统目录视图重建，在空库里运行能还原列（类型长度、精度与
// 小数位、IDENTITY、默认值）、主键、唯一约束、CHECK，以及索引（单独成句）。外键、计算列、
// 列注释（扩展属性）不在其中。表名是 "schema.table"（裸名按 dbo 查询），语句里的表名保持收到的写法。
var _ sqldb.DDL = dialect{}

const ddlColumnsQuery = "SELECT c.name, t.name, c.max_length, c.precision, c.scale, c.is_nullable, " +
	"CAST(ic.seed_value AS BIGINT), CAST(ic.increment_value AS BIGINT), dc.definition " +
	"FROM sys.columns c JOIN sys.types t ON t.user_type_id = c.user_type_id " +
	"LEFT JOIN sys.identity_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id " +
	"LEFT JOIN sys.default_constraints dc ON dc.object_id = c.default_object_id " +
	"WHERE c.object_id = OBJECT_ID(@p1) ORDER BY c.column_id"

const ddlIndexesQuery = "SELECT i.name, i.is_primary_key, i.is_unique_constraint, i.is_unique, i.type, i.filter_definition, " +
	"col.name, ixc.is_descending_key, ixc.is_included_column " +
	"FROM sys.indexes i JOIN sys.index_columns ixc ON ixc.object_id = i.object_id AND ixc.index_id = i.index_id " +
	"JOIN sys.columns col ON col.object_id = ixc.object_id AND col.column_id = ixc.column_id " +
	"WHERE i.object_id = OBJECT_ID(@p1) AND i.type IN (1, 2) AND i.is_hypothetical = 0 " +
	"ORDER BY i.index_id, ixc.is_included_column, ixc.key_ordinal, ixc.index_column_id"

const ddlChecksQuery = "SELECT name, definition FROM sys.check_constraints WHERE parent_object_id = OBJECT_ID(@p1) ORDER BY object_id"

type ddlColumn struct {
	name, typeName              string
	maxLength, precision, scale int64
	nullable                    bool
	identitySeed, identityStep  sql.NullInt64
	defaultDefinition           sql.NullString
}

// ddlIndex 是一个索引或主键 / 唯一约束，行按索引名聚合。
type ddlIndex struct {
	name                    string
	primaryKey, uniqueConst bool
	unique, clustered       bool
	filter                  string
	keys, included          []string
}

func (dialect) TableDDL(ctx context.Context, conn *sql.Conn, _, table string) (string, error) {
	schema, tbl := splitSchemaTable(table)
	object := quoteIdent(schema) + "." + quoteIdent(tbl)
	columns, err := queryDDLColumns(ctx, conn, object)
	if err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", nil
	}
	indexes, err := queryDDLIndexes(ctx, conn, object)
	if err != nil {
		return "", err
	}
	checks, err := queryDDLChecks(ctx, conn, object)
	if err != nil {
		return "", err
	}

	defs := make([]string, 0, len(columns)+len(indexes)+len(checks))
	for _, column := range columns {
		defs = append(defs, columnDefinition(column))
	}
	tableRef := sqldb.QuoteSchemaTable(table, quoteIdent)
	var indexStatements []string
	for _, index := range indexes {
		switch {
		case index.primaryKey:
			defs = append(defs, constraintDefinition(index, "PRIMARY KEY"))
		case index.uniqueConst:
			defs = append(defs, constraintDefinition(index, "UNIQUE"))
		default:
			indexStatements = append(indexStatements, indexStatement(tableRef, index))
		}
	}
	for _, check := range checks {
		defs = append(defs, "CONSTRAINT "+quoteIdent(check[0])+" CHECK "+check[1])
	}
	statements := append([]string{fmt.Sprintf("CREATE TABLE %s (\n  %s\n);", tableRef, strings.Join(defs, ",\n  "))}, indexStatements...)
	return strings.Join(statements, "\n"), nil
}

func queryDDLColumns(ctx context.Context, conn *sql.Conn, object string) ([]ddlColumn, error) {
	rows, err := conn.QueryContext(ctx, ddlColumnsQuery, sql.Named("p1", object))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var columns []ddlColumn
	for rows.Next() {
		var c ddlColumn
		if err := rows.Scan(&c.name, &c.typeName, &c.maxLength, &c.precision, &c.scale, &c.nullable,
			&c.identitySeed, &c.identityStep, &c.defaultDefinition); err != nil {
			return nil, err
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}

func queryDDLIndexes(ctx context.Context, conn *sql.Conn, object string) ([]*ddlIndex, error) {
	rows, err := conn.QueryContext(ctx, ddlIndexesQuery, sql.Named("p1", object))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var indexes []*ddlIndex
	for rows.Next() {
		var (
			name, column                 string
			primary, uniqueConst, unique bool
			descending, included         bool
			indexType                    int64
			filter                       sql.NullString
		)
		if err := rows.Scan(&name, &primary, &uniqueConst, &unique, &indexType, &filter, &column, &descending, &included); err != nil {
			return nil, err
		}
		if len(indexes) == 0 || indexes[len(indexes)-1].name != name {
			indexes = append(indexes, &ddlIndex{
				name: name, primaryKey: primary, uniqueConst: uniqueConst, unique: unique,
				clustered: indexType == 1, filter: filter.String,
			})
		}
		index := indexes[len(indexes)-1]
		switch {
		case included:
			index.included = append(index.included, quoteIdent(column))
		case descending:
			index.keys = append(index.keys, quoteIdent(column)+" DESC")
		default:
			index.keys = append(index.keys, quoteIdent(column))
		}
	}
	return indexes, rows.Err()
}

func queryDDLChecks(ctx context.Context, conn *sql.Conn, object string) ([][2]string, error) {
	rows, err := conn.QueryContext(ctx, ddlChecksQuery, sql.Named("p1", object))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var checks [][2]string
	for rows.Next() {
		var check [2]string
		if err := rows.Scan(&check[0], &check[1]); err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

func columnDefinition(c ddlColumn) string {
	def := quoteIdent(c.name) + " " + columnType(c)
	if c.identitySeed.Valid {
		def += fmt.Sprintf(" IDENTITY(%d, %d)", c.identitySeed.Int64, c.identityStep.Int64)
	}
	if c.nullable {
		def += " NULL"
	} else {
		def += " NOT NULL"
	}
	if c.defaultDefinition.Valid {
		def += " DEFAULT " + c.defaultDefinition.String
	}
	return def
}

// columnType 把 sys.columns 的 max_length / precision / scale 写回类型。max_length 是字节数，
// n 开头的 Unicode 类型每字符两字节；-1 表示 (max)。
func columnType(c ddlColumn) string {
	switch c.typeName {
	case "char", "varchar", "binary", "varbinary":
		return c.typeName + lengthSuffix(c.maxLength)
	case "nchar", "nvarchar":
		if c.maxLength > 0 {
			return fmt.Sprintf("%s(%d)", c.typeName, c.maxLength/2)
		}
		return c.typeName + lengthSuffix(c.maxLength)
	case "decimal", "numeric":
		return fmt.Sprintf("%s(%d, %d)", c.typeName, c.precision, c.scale)
	case "datetime2", "datetimeoffset", "time":
		return fmt.Sprintf("%s(%d)", c.typeName, c.scale)
	case "float":
		if c.precision != 53 {
			return fmt.Sprintf("float(%d)", c.precision)
		}
	}
	return c.typeName
}

func lengthSuffix(maxLength int64) string {
	if maxLength == -1 {
		return "(max)"
	}
	return fmt.Sprintf("(%d)", maxLength)
}

func constraintDefinition(index *ddlIndex, kind string) string {
	return fmt.Sprintf("CONSTRAINT %s %s %s (%s)", quoteIdent(index.name), kind, indexKind(index), strings.Join(index.keys, ", "))
}

func indexStatement(tableRef string, index *ddlIndex) string {
	stmt := "CREATE "
	if index.unique {
		stmt += "UNIQUE "
	}
	stmt += fmt.Sprintf("%s INDEX %s ON %s (%s)", indexKind(index), quoteIdent(index.name), tableRef, strings.Join(index.keys, ", "))
	if len(index.included) > 0 {
		stmt += " INCLUDE (" + strings.Join(index.included, ", ") + ")"
	}
	if index.filter != "" {
		stmt += " WHERE " + index.filter
	}
	return stmt + ";"
}

func indexKind(index *ddlIndex) string {
	if index.clustered {
		return "CLUSTERED"
	}
	return "NONCLUSTERED"
}
