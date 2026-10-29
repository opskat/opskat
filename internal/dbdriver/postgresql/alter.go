package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 改表：列按表所在的 schema 查 information_schema（自定义类型显示类型名），注释用 col_description /
// obj_description；每项改动一条语句：改名 RENAME TO 只给新名字（表留在原 schema），列的类型、可空、
// 默认值各自 ALTER COLUMN，注释用 COMMENT ON。表注释与列注释都能改。索引照 indexesSQL 读（见 readIndexes），
// 删索引写在列的改动之前、建索引写在之后（见 alterIndexes）。
var _ sqldb.TableAlterer = dialect{}

const (
	alterColumnsSQL = "SELECT column_name, data_type, udt_name, is_nullable, column_default, " +
		"COALESCE(col_description(format('%I.%I', table_schema, table_name)::regclass, ordinal_position::int), '') AS column_comment, " +
		"is_identity " +
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
				// 自增的现状：IDENTITY 列与 serial 列（只显示，已有列的自增不能改）。
				AutoIncrement: autoIncrementing(row),
			},
			Row: row,
		})
	}
	err = conn.QueryRowContext(ctx, alterTableCommentSQL, schema, name).Scan(&out.Comment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	indexes, err := readIndexes(ctx, conn, schema, name)
	if err != nil {
		return nil, fmt.Errorf("indexes: %w", err)
	}
	for _, index := range indexes {
		out.Indexes = append(out.Indexes, index.indexDefinition())
	}
	if out.TakenIndexNames, err = sqldb.QueryNames(ctx, conn, takenIndexNamesSQL, schema, name); err != nil {
		return nil, fmt.Errorf("index names in schema %s: %w", schema, err)
	}
	return out, nil
}

func (d dialect) AlterTableSQL(ctx context.Context, conn *sql.Conn, change sqldb.TableChange) ([]string, error) {
	literals := sqldb.DefinitionLiterals
	target := change.TargetRef
	var statements, additions, renames, modifications, comments []string
	if change.Rename != "" {
		statements = append(statements, "ALTER TABLE "+change.TableRef+" RENAME TO "+quoteIdent(change.Rename))
	}
	indexDrops, indexCreates, err := alterIndexes(ctx, conn, change)
	if err != nil {
		return nil, err
	}
	statements = append(statements, indexDrops...)
	commentOn := func(name, comment string) (string, error) {
		return d.ColumnCommentSQL(target, dbdriver.ColumnDefinition{Name: name, Comment: comment})
	}

	for _, column := range change.Columns {
		c := column.Column
		name := quoteIdent(c.Name)
		alter := "ALTER TABLE " + target + " ALTER COLUMN " + name
		if column.IsNew() {
			definition, err := sqldb.NewColumnSQL(d, literals, quoteIdent, c)
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
			// 按写法写（没给写法时照旧按文本猜）；没有默认值是 DROP DEFAULT。
			literal, ok, err := sqldb.DefaultSQL(literals, c)
			if err != nil {
				return nil, err
			}
			if ok {
				modifications = append(modifications, alter+" SET DEFAULT "+literal)
			} else {
				modifications = append(modifications, alter+" DROP DEFAULT")
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

	// 先删列、再改名、然后加列：被删列的名字可以给改名的列或新列用，改名让出的旧名字可以给新列用。
	for _, drop := range change.Drops {
		statements = append(statements, "ALTER TABLE "+target+" DROP COLUMN "+quoteIdent(drop))
	}
	statements = append(statements, renames...)
	statements = append(statements, additions...)
	statements = append(statements, modifications...)
	statements = append(statements, indexCreates...)
	if change.Comment != nil {
		statement, err := d.TableCommentSQL(target, *change.Comment)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return append(statements, comments...), nil
}
