package postgresql

import (
	"context"
	sqldriver "database/sql/driver"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// familyRows 是表在 pg_inherits 里的父表（"parent"）与分区（"partition"）。
func familyRows(relations ...[2]string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"relation", "name"})
	for _, r := range relations {
		rows.AddRow(r[0], r[1])
	}
	return rows
}

// ddlExpectation 是 public 里一张表的建表语句读到的内容。
type ddlExpectation struct {
	name string
	rel  ddlRel
	cols []ddlCol
	// partitioned 表示它是分区表或分区（还要读分区键 key 与分区范围 bound）。
	partitioned bool
	key, bound  any
	sequences   [][9]sqldriver.Value
	constraints [][2]string
	indexes     []string
}

func expectDDL(mock sqlmock.Sqlmock, e ddlExpectation) {
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("public", e.name).WillReturnRows(ddlRelationRows(e.rel, e.cols...))
	if e.partitioned {
		mock.ExpectQuery(partitionQuery).WithArgs("public", e.name).WillReturnRows(partitionRows(e.key, e.bound))
	}
	sequences := ownedSequenceRows()
	for _, s := range e.sequences {
		sequences.AddRow(s[:]...)
	}
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("public", e.name).WillReturnRows(sequences)
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("public", e.name).WillReturnRows(constraintRows(e.constraints...))
	mock.ExpectQuery(tableIndexesQuery).WithArgs("public", e.name).WillReturnRows(names("def", e.indexes...))
}

// 分区表与继承表的整库转储（结构和数据，勾了写入删除语句）：分区表的父表 readings 带分区键，分区写成 PARTITION OF
// （r_2026 再按地区分区，r_default 是默认分区），继承的子表 a_child 写成 INHERITS 并只列它自己的列；分区与子表排在
// 父表之后（列出时在前），删除段里先删子表再删父表；父表上的外键只在父表上加一次（分区上随之建出的不再写，查询
// 已去掉）；每张表只读它自己的行（ONLY），分区表的父表与再分区的分区没有记录段。
func TestDumpWritesPartitionedAndInheritedTables(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(viewsSQL).WillReturnRows(names("v"))
	mock.ExpectQuery(proceduresSQL).WillReturnRows(names("r"))
	mock.ExpectQuery(functionsSQL).WillReturnRows(names("r"))
	mock.ExpectQuery(triggersSQL).WillReturnRows(sqlmock.NewRows([]string{"s", "n", "t"}))
	mock.ExpectQuery(listTablesSQL).WillReturnRows(names("name",
		"public.a_child", "public.base", "public.r_2026", "public.r_2026_eu", "public.r_default", "public.readings"))

	inherited := func(name, dataType string) ddlCol { return ddlCol{name: name, dataType: dataType, inherited: true} }
	partitionColumns := []ddlCol{inherited("id", "integer"), inherited("created", "date"), inherited("region", "text")}
	partitionData := []string{"id", "created", "region"}
	readingsParent := `[["public", "readings"]]`
	noSequences := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"attname", "last_value"}) }
	expectTable := func(ddl ddlExpectation, family, foreignKeys *sqlmock.Rows, columns []string, sequences *sqlmock.Rows) {
		expectDDL(mock, ddl)
		mock.ExpectQuery(dumpFamilyQuery).WithArgs("public", ddl.name).WillReturnRows(family)
		mock.ExpectQuery(dumpForeignKeysQuery).WithArgs("public", ddl.name).WillReturnRows(foreignKeys)
		mock.ExpectQuery(dumpColumnsQuery).WithArgs("public", ddl.name).WillReturnRows(dumpColumnRows(columns...))
		mock.ExpectQuery(dumpSequencesQuery).WithArgs("public", ddl.name).WillReturnRows(sequences)
	}
	expectTable(ddlExpectation{name: "a_child", rel: ddlRel{kind: "r", parents: `[["public", "base"]]`},
		cols: []ddlCol{inherited("id", "integer"), inherited("name", "text"), {name: "extra", dataType: "integer", nullable: true}}},
		familyRows([2]string{"parent", "public.base"}), constraintRows(), []string{"id", "name", "extra"}, noSequences())
	expectTable(ddlExpectation{name: "base", rel: ddlRel{kind: "r"},
		cols: []ddlCol{
			{name: "id", dataType: "integer", def: "nextval('public.base_id_seq'::regclass)"},
			{name: "name", dataType: "text", nullable: true},
		},
		sequences:   [][9]sqldriver.Value{{"public", "base_id_seq", "id", "integer", "1", "1", "1", "2147483647", false}},
		constraints: [][2]string{{"base_pkey", "PRIMARY KEY (id)"}}},
		familyRows(), constraintRows(), []string{"id", "name"},
		sqlmock.NewRows([]string{"attname", "last_value"}).AddRow("id", 2))
	expectTable(ddlExpectation{name: "r_2026", rel: ddlRel{kind: "p", partition: true, parents: readingsParent}, cols: partitionColumns,
		partitioned: true, key: "LIST (region)", bound: "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
		indexes: []string{"CREATE INDEX r_2026_only ON public.r_2026 USING btree (id)"}},
		familyRows([2]string{"parent", "public.readings"}, [2]string{"partition", "public.r_2026_eu"}), constraintRows(),
		partitionData, noSequences())
	expectTable(ddlExpectation{name: "r_2026_eu", rel: ddlRel{kind: "r", partition: true, parents: `[["public", "r_2026"]]`},
		cols: partitionColumns, partitioned: true, bound: "FOR VALUES IN ('eu')"},
		familyRows([2]string{"parent", "public.r_2026"}), constraintRows(), partitionData, noSequences())
	expectTable(ddlExpectation{name: "r_default", rel: ddlRel{kind: "r", partition: true, parents: readingsParent},
		cols: partitionColumns, partitioned: true, bound: "DEFAULT"},
		familyRows([2]string{"parent", "public.readings"}), constraintRows(), partitionData, noSequences())
	expectTable(ddlExpectation{name: "readings", rel: ddlRel{kind: "p"},
		cols:        []ddlCol{{name: "id", dataType: "integer"}, {name: "created", dataType: "date"}, {name: "region", dataType: "text"}},
		partitioned: true, key: "RANGE (created)",
		constraints: [][2]string{{"readings_pkey", "PRIMARY KEY (id, created, region)"}},
		indexes:     []string{"CREATE INDEX readings_region ON public.readings USING btree (region)"}},
		familyRows([2]string{"partition", "public.r_2026"}, [2]string{"partition", "public.r_default"}),
		constraintRows([2]string{"readings_base_fk", "FOREIGN KEY (id) REFERENCES base(id)"}), partitionData, noSequences())

	// 全部定义读完才取整份文件的部分。
	mock.ExpectQuery(dumpSchemasQuery).WillReturnRows(names("nspname"))
	mock.ExpectQuery(scopeEnumsQuery).WithArgs("",
		`["public.a_child","public.base","public.r_2026","public.r_2026_eu","public.r_default","public.readings"]`).WillReturnRows(enumRows())

	// 按写出的次序读行，每张表只读它自己的；分区表（readings、r_2026）的行都在分区里，不读。
	types := map[string]string{"id": "integer", "extra": "integer", "created": "date", "region": "text", "name": "text"}
	expectRows := func(name string, columns []string, rows ...[]any) {
		typed := make([][2]string, len(columns))
		for i, column := range columns {
			typed[i] = [2]string{column, types[column]}
		}
		expectColumns(mock, "public", name, typed...)
		result := sqlmock.NewRows(columns)
		for _, row := range rows {
			values := make([]sqldriver.Value, len(row))
			for i, value := range row {
				values[i] = value
			}
			result.AddRow(values...)
		}
		mock.ExpectQuery(`SELECT * FROM ONLY "public"."` + name + `"`).WillReturnRows(result)
	}
	expectRows("base", []string{"id", "name"}, []any{int64(1), "parent row"})
	expectRows("a_child", []string{"id", "name", "extra"}, []any{int64(2), "child row", int64(7)})
	expectRows("r_2026_eu", partitionData, []any{int64(1), "2026-03-01", "eu"})
	expectRows("r_default", partitionData, []any{int64(2), "2030-01-01", "us"})

	var out strings.Builder
	result, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "shop", Whole: true, WithData: true, Header: dumpHeader(), Writer: &out,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Drops: true},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.EqualValues(t, 4, result.Rows)
	assert.Equal(t, readGolden(t, "dump_partitions.sql"), out.String())
}
