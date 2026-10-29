package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 改表：列照 pragma_table_info 读，SQLite 没有注释。ALTER TABLE 只能改表名、新增列、给列改名与删除列，
// 已有列的类型、可空、默认值都改不了，所以一项属性也不声明。rowid 主键（INTEGER PRIMARY KEY）报告为自增。
// 索引照 readIndexes 读（见 tableIndexes），删索引写在列的改动之前、建索引写在之后。
var _ sqldb.TableAlterer = dialect{}

func (dialect) AlterAttributes() []dbdriver.AlterAttribute { return alterAttributes }

var alterAttributes = []dbdriver.AlterAttribute{}

func (dialect) AlterableTable(ctx context.Context, conn *sql.Conn, database, table string) (*sqldb.AlterableTable, error) {
	records, err := sqldb.QueryRecords(ctx, conn,
		`SELECT name, type, "notnull", dflt_value, pk FROM `+qualify(database, "pragma_table_info(?)"), table)
	if err != nil {
		return nil, err
	}
	out := &sqldb.AlterableTable{Name: table, Columns: make([]sqldb.AlterableColumn, 0, len(records))}
	// primaryKeys 是主键列，按在主键里的次序（pk 是从 1 起的位置，不是主键列的为 0）。
	var primaryKeys []string
	var positions []int
	for _, row := range records {
		name := sqldb.PickString(row, "name")
		position, err := strconv.Atoi(sqldb.PickString(row, "pk"))
		if err != nil {
			return nil, fmt.Errorf("column %q: primary key position: %w", name, err)
		}
		if position > 0 {
			i, _ := slices.BinarySearch(positions, position)
			positions = slices.Insert(positions, i, position)
			primaryKeys = slices.Insert(primaryKeys, i, name)
		}
		out.Columns = append(out.Columns, sqldb.AlterableColumn{
			ColumnDefinition: dbdriver.ColumnDefinition{
				Name:         name,
				Type:         sqldb.PickString(row, "type"),
				Nullable:     sqldb.PickString(row, "notnull") != "1",
				DefaultValue: sqldb.PickString(row, "dflt_value"),
			},
			Row: row,
		})
	}
	if len(out.Columns) == 0 {
		return out, nil
	}
	indexes, err := readIndexes(ctx, conn, database, table)
	if err != nil {
		return nil, fmt.Errorf("indexes: %w", err)
	}
	if rowidAlias(indexes, primaryKeys) {
		for i := range out.Columns {
			out.Columns[i].AutoIncrement = out.Columns[i].Name == primaryKeys[0]
		}
	}
	out.Indexes = tableIndexes(indexes, primaryKeys)
	if out.TakenIndexNames, err = sqldb.QueryNames(ctx, conn, takenIndexNamesSQL(database), table); err != nil {
		return nil, fmt.Errorf("taken index names: %w", err)
	}
	return out, nil
}

// AlterTableSQL 按次序写：改表名，删索引（请求删的、随列删的、要重建的），新增列，列改名，删列，建索引。
// 新增的列不能自增（会话已按 AutoIncrementsNewTablesOnly 拒绝），默认值不是常量时表里不能有行
// （checkAddColumnDefault）。列改名时 SQLite 把索引里的列名一起改掉，引用它的索引不必重建。
func (d dialect) AlterTableSQL(ctx context.Context, conn *sql.Conn, change sqldb.TableChange) ([]string, error) {
	literals := sqldb.DefinitionLiterals
	target := change.TargetRef
	var statements, renames []string
	if change.Rename != "" {
		statements = append(statements, "ALTER TABLE "+change.TableRef+" RENAME TO "+quoteIdent(change.Rename))
	}
	indexDrops, err := sqldb.IndexDropStatements(d, target, change)
	if err != nil {
		return nil, err
	}
	statements = append(statements, indexDrops...)
	// 先删列、再改名、最后加列：被删列的名字可以给改名的列或新列用，改名让出的旧名字可以给新列用。
	for _, drop := range change.Drops {
		statements = append(statements, "ALTER TABLE "+target+" DROP COLUMN "+quoteIdent(drop))
	}
	var additions []string
	for _, column := range change.Columns {
		if column.IsNew() {
			if err := checkAddColumnDefault(ctx, conn, change.TableRef, column.Column); err != nil {
				return nil, err
			}
			definition, err := sqldb.ColumnDefinitionSQL(literals, quoteIdent, withDefaultExpression(column.Column))
			if err != nil {
				return nil, err
			}
			additions = append(additions, "ALTER TABLE "+target+" ADD COLUMN "+definition)
			continue
		}
		// 能走到这里的已有列只有改名（其余属性未声明，Session 已拒绝）。
		renames = append(renames, "ALTER TABLE "+target+" RENAME COLUMN "+quoteIdent(column.Current.Name)+" TO "+quoteIdent(column.Column.Name))
	}
	statements = append(statements, renames...)
	statements = append(statements, additions...)
	indexCreates, err := sqldb.IndexCreateStatements(d, target, change)
	if err != nil {
		return nil, err
	}
	return append(statements, indexCreates...), nil
}
