package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 改表：列按表所在的 schema 查 INFORMATION_SCHEMA（裸名按 dbo），类型带上长度（-1 写成 max）。改表名与
// 列名用 sp_rename（新表名不带 schema，表留在原 schema），新增列 ADD
// 不带 COLUMN 关键字，改类型与可空性用 ALTER COLUMN（必须同时给出类型）。默认值要经命名约束、注释要经
// 扩展属性，都不在此修改，所以只能改已有列的类型与可空性，注释也不读。
var _ sqldb.TableAlterer = dialect{}

const alterColumnsSQL = "SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, IS_NULLABLE, COLUMN_DEFAULT " +
	"FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 ORDER BY ORDINAL_POSITION"

var alterAttributes = []dbdriver.AlterAttribute{dbdriver.AlterColumnType, dbdriver.AlterColumnNullable}

func (dialect) AlterAttributes() []dbdriver.AlterAttribute { return alterAttributes }

func (dialect) AlterableTable(ctx context.Context, conn *sql.Conn, _, table string) (*sqldb.AlterableTable, error) {
	schema, name := splitSchemaTable(table)
	records, err := sqldb.QueryRecords(ctx, conn, alterColumnsSQL, sql.Named("p1", schema), sql.Named("p2", name))
	if err != nil {
		return nil, err
	}
	out := &sqldb.AlterableTable{Name: name, Columns: make([]sqldb.AlterableColumn, 0, len(records))}
	for _, row := range records {
		columnType := sqldb.PickString(row, "DATA_TYPE")
		switch length := sqldb.PickString(row, "CHARACTER_MAXIMUM_LENGTH"); {
		case length == "-1":
			columnType += "(max)"
		case length != "" && length != "0":
			columnType += "(" + length + ")"
		}
		out.Columns = append(out.Columns, sqldb.AlterableColumn{
			ColumnDefinition: dbdriver.ColumnDefinition{
				Name:         sqldb.PickString(row, "COLUMN_NAME"),
				Type:         columnType,
				Nullable:     strings.EqualFold(sqldb.PickString(row, "IS_NULLABLE"), "YES"),
				DefaultValue: sqldb.PickString(row, "COLUMN_DEFAULT"),
			},
			Row: row,
		})
	}
	return out, nil
}

func (dialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change sqldb.TableChange) ([]string, error) {
	literals := sqldb.DefinitionLiterals
	name := func(text string) (string, error) { return literals.Literal(text, "") }
	target := change.TargetRef

	var statements, renames, modifications []string
	if change.Rename != "" {
		from, err := name(change.Table)
		if err != nil {
			return nil, err
		}
		to, err := name(change.Rename)
		if err != nil {
			return nil, err
		}
		statements = append(statements, fmt.Sprintf("EXEC sp_rename %s, %s", from, to))
	}
	for _, column := range change.Columns {
		c := column.Column
		if column.IsNew() {
			definition, err := sqldb.ColumnDefinitionSQL(literals, quoteIdent, c)
			if err != nil {
				return nil, err
			}
			statements = append(statements, "ALTER TABLE "+target+" ADD "+definition)
			continue
		}
		if column.Renamed {
			// 列的对象名要带（改名之后的）表名作前缀。
			from, err := name(change.Target + "." + column.Current.Name)
			if err != nil {
				return nil, err
			}
			to, err := name(c.Name)
			if err != nil {
				return nil, err
			}
			renames = append(renames, fmt.Sprintf("EXEC sp_rename %s, %s, 'COLUMN'", from, to))
		}
		if column.TypeChanged || column.NullableChanged {
			nullable := " NOT NULL"
			if c.Nullable {
				nullable = " NULL"
			}
			modifications = append(modifications, "ALTER TABLE "+target+" ALTER COLUMN "+quoteIdent(c.Name)+" "+strings.TrimSpace(c.Type)+nullable)
		}
	}
	statements = append(statements, renames...)
	statements = append(statements, modifications...)
	for _, drop := range change.Drops {
		statements = append(statements, "ALTER TABLE "+target+" DROP COLUMN "+quoteIdent(drop))
	}
	return statements, nil
}
