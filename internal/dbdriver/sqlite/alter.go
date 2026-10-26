package sqlite

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 改表：列照 pragma_table_info 读，SQLite 没有注释。ALTER TABLE 只能改表名、新增列、给列改名与删除列，
// 已有列的类型、可空、默认值都改不了，所以一项属性也不声明。
var _ sqldb.TableAlterer = dialect{}

func (dialect) AlterAttributes() []dbdriver.AlterAttribute { return alterAttributes }

var alterAttributes = []dbdriver.AlterAttribute{}

func (dialect) AlterableTable(ctx context.Context, conn *sql.Conn, database, table string) (*sqldb.AlterableTable, error) {
	records, err := sqldb.QueryRecords(ctx, conn,
		`SELECT name, type, "notnull", dflt_value FROM `+qualify(database, "pragma_table_info(?)"), table)
	if err != nil {
		return nil, err
	}
	out := &sqldb.AlterableTable{Name: table, Columns: make([]sqldb.AlterableColumn, 0, len(records))}
	for _, row := range records {
		out.Columns = append(out.Columns, sqldb.AlterableColumn{
			ColumnDefinition: dbdriver.ColumnDefinition{
				Name:         sqldb.PickString(row, "name"),
				Type:         sqldb.PickString(row, "type"),
				Nullable:     sqldb.PickString(row, "notnull") != "1",
				DefaultValue: sqldb.PickString(row, "dflt_value"),
			},
			Row: row,
		})
	}
	return out, nil
}

func (dialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change sqldb.TableChange) ([]string, error) {
	literals := sqldb.DefinitionLiterals
	target := change.TargetRef
	var statements, renames []string
	if change.Rename != "" {
		statements = append(statements, "ALTER TABLE "+change.TableRef+" RENAME TO "+quoteIdent(change.Rename))
	}
	for _, column := range change.Columns {
		if column.IsNew() {
			definition, err := sqldb.ColumnDefinitionSQL(literals, quoteIdent, column.Column)
			if err != nil {
				return nil, err
			}
			statements = append(statements, "ALTER TABLE "+target+" ADD COLUMN "+definition)
			continue
		}
		// 能走到这里的已有列只有改名（其余属性未声明，Session 已拒绝）。
		renames = append(renames, "ALTER TABLE "+target+" RENAME COLUMN "+quoteIdent(column.Current.Name)+" TO "+quoteIdent(column.Column.Name))
	}
	statements = append(statements, renames...)
	for _, drop := range change.Drops {
		statements = append(statements, "ALTER TABLE "+target+" DROP COLUMN "+quoteIdent(drop))
	}
	return statements, nil
}
