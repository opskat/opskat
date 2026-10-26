package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 改表：列按表所在的 schema 查 information_schema（自定义类型显示类型名），注释用 col_description /
// obj_description；每项改动一条语句：改名 RENAME TO 只给新名字（表留在原 schema），列的类型、可空、
// 默认值各自 ALTER COLUMN，注释用 COMMENT ON。表注释与列注释都能改。
var _ sqldb.TableAlterer = dialect{}

const (
	alterColumnsSQL = "SELECT column_name, data_type, udt_name, is_nullable, column_default, " +
		"COALESCE(col_description(format('%I.%I', table_schema, table_name)::regclass, ordinal_position::int), '') AS column_comment " +
		"FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position"
	alterTableCommentSQL = "SELECT COALESCE(obj_description(c.oid, 'pg_class'), '') AS table_comment " +
		"FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace " +
		"WHERE n.nspname = $1 AND c.relname = $2"
)

var alterAttributes = []dbdriver.AlterAttribute{
	dbdriver.AlterTableComment, dbdriver.AlterColumnType, dbdriver.AlterColumnNullable,
	dbdriver.AlterColumnDefault, dbdriver.AlterColumnComment,
}

func (dialect) AlterAttributes() []dbdriver.AlterAttribute { return alterAttributes }

func (dialect) AlterableTable(ctx context.Context, conn *sql.Conn, _, table string) (*sqldb.AlterableTable, error) {
	schema, name := splitSchemaTable(table)
	records, err := sqldb.QueryRecords(ctx, conn, alterColumnsSQL, schema, name)
	if err != nil {
		return nil, err
	}
	out := &sqldb.AlterableTable{Name: name, Columns: make([]sqldb.AlterableColumn, 0, len(records))}
	for _, row := range records {
		columnType := sqldb.PickString(row, "data_type")
		if udt := sqldb.PickString(row, "udt_name"); columnType == "USER-DEFINED" && udt != "" {
			columnType = udt
		}
		out.Columns = append(out.Columns, sqldb.AlterableColumn{
			ColumnDefinition: dbdriver.ColumnDefinition{
				Name:         sqldb.PickString(row, "column_name"),
				Type:         columnType,
				Nullable:     strings.EqualFold(sqldb.PickString(row, "is_nullable"), "YES"),
				DefaultValue: sqldb.PickString(row, "column_default"),
				Comment:      sqldb.PickString(row, "column_comment"),
			},
			Row: row,
		})
	}
	err = conn.QueryRowContext(ctx, alterTableCommentSQL, schema, name).Scan(&out.Comment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

func (dialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change sqldb.TableChange) ([]string, error) {
	literals := sqldb.DefinitionLiterals
	target := change.TargetRef
	var statements, additions, renames, modifications, comments []string
	if change.Rename != "" {
		statements = append(statements, "ALTER TABLE "+change.TableRef+" RENAME TO "+quoteIdent(change.Rename))
	}
	commentOn := func(name, comment string) (string, error) {
		value := "NULL"
		if strings.TrimSpace(comment) != "" {
			literal, err := literals.Literal(comment, "")
			if err != nil {
				return "", err
			}
			value = literal
		}
		return "COMMENT ON COLUMN " + target + "." + quoteIdent(name) + " IS " + value, nil
	}

	for _, column := range change.Columns {
		c := column.Column
		name := quoteIdent(c.Name)
		alter := "ALTER TABLE " + target + " ALTER COLUMN " + name
		if column.IsNew() {
			definition, err := sqldb.ColumnDefinitionSQL(literals, quoteIdent, c)
			if err != nil {
				return nil, err
			}
			additions = append(additions, "ALTER TABLE "+target+" ADD COLUMN "+definition)
			if strings.TrimSpace(c.Comment) != "" {
				statement, err := commentOn(c.Name, c.Comment)
				if err != nil {
					return nil, err
				}
				comments = append(comments, statement)
			}
			continue
		}
		if column.Renamed {
			renames = append(renames, "ALTER TABLE "+target+" RENAME COLUMN "+quoteIdent(column.Current.Name)+" TO "+name)
		}
		if column.TypeChanged {
			modifications = append(modifications, alter+" TYPE "+strings.TrimSpace(c.Type))
		}
		if column.NullableChanged {
			if c.Nullable {
				modifications = append(modifications, alter+" DROP NOT NULL")
			} else {
				modifications = append(modifications, alter+" SET NOT NULL")
			}
		}
		if column.DefaultChanged {
			if strings.TrimSpace(c.DefaultValue) == "" {
				modifications = append(modifications, alter+" DROP DEFAULT")
			} else {
				literal, err := literals.Default(c.DefaultValue, c.Type)
				if err != nil {
					return nil, err
				}
				modifications = append(modifications, alter+" SET DEFAULT "+literal)
			}
		}
		if column.CommentChanged {
			statement, err := commentOn(c.Name, c.Comment)
			if err != nil {
				return nil, err
			}
			comments = append(comments, statement)
		}
	}

	statements = append(statements, additions...)
	statements = append(statements, renames...)
	statements = append(statements, modifications...)
	for _, drop := range change.Drops {
		statements = append(statements, "ALTER TABLE "+target+" DROP COLUMN "+quoteIdent(drop))
	}
	if change.Comment != nil {
		value := "NULL"
		if strings.TrimSpace(*change.Comment) != "" {
			literal, err := literals.Literal(*change.Comment, "")
			if err != nil {
				return nil, err
			}
			value = literal
		}
		statements = append(statements, "COMMENT ON TABLE "+target+" IS "+value)
	}
	return append(statements, comments...), nil
}
