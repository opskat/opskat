package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// designerDialect 在 tablesDialect 之上实现默认值写法的判定与自增：表设计器的两项列能力都开启。
type designerDialect struct{ tablesDialect }

// ClassifyDefault 按测试自己的目录写法判定：空是无，NULL 是 NULL，'…'::type 是值，其余是表达式。
func (designerDialect) ClassifyDefault(column AlterableColumn) (dbdriver.DefaultKind, string) {
	raw := column.DefaultValue
	switch {
	case raw == "":
		return dbdriver.DefaultKindNone, ""
	case raw == "NULL":
		return dbdriver.DefaultKindNull, ""
	case strings.HasPrefix(raw, "'"):
		value, _, _ := strings.Cut(strings.TrimPrefix(raw, "'"), "'::")
		return dbdriver.DefaultKindValue, value
	}
	return dbdriver.DefaultKindExpression, raw
}

// AutoIncrementSQL 只让整数列自增。
func (designerDialect) AutoIncrementSQL(column dbdriver.ColumnDefinition) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(column.Type), "int") {
		return "", fmt.Errorf("column %q of type %s cannot auto-increment", column.Name, column.Type)
	}
	return "AUTO_INC", nil
}

func newDesignerSession(t *testing.T) *Session {
	t.Helper()
	db, _, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, designerDialect{})
}

// 方言实现了判定与自增，驱动描述的两项声明就从同一个方言得出；没实现的都不声明。
func TestColumnDeclarationsComeFromTheDialect(t *testing.T) {
	assert.True(t, ClassifiesDefaults(designerDialect{}))
	assert.True(t, AutoIncrements(designerDialect{}))
	assert.False(t, ClassifiesDefaults(tablesDialect{}))
	assert.False(t, AutoIncrements(tablesDialect{}))
}

// 方言没开启的列能力，建表在生成任何语句之前以 ErrUnsupported 拒绝，不碰数据库。
func TestCreateTableRefusesColumnFeaturesTheDialectLacks(t *testing.T) {
	session, mock := newTablesSession(t)
	ctx := context.Background()
	for name, column := range map[string]dbdriver.NewColumn{
		"default kind":   {Name: "at", Type: "timestamp", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "now()"},
		"auto-increment": {Name: "id", Type: "int", AutoIncrement: true},
	} {
		_, err := session.CreateTableStatements(ctx, "db", dbdriver.CreateTableRequest{Name: "t", Columns: []dbdriver.NewColumn{column}})
		require.ErrorIs(t, err, dbdriver.ErrUnsupported, name)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// 开启了默认值写法的方言按写法写 DEFAULT：无不写，NULL 写 NULL，值按字面量（不再猜，函数名也加引号），
// 表达式原样；没给写法的列照旧猜。自增子句由方言给出，写在类型之后。
func TestCreateTableWritesDefaultsByKindAndTheDialectsAutoIncrement(t *testing.T) {
	statements, err := newDesignerSession(t).CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "int", AutoIncrement: true},
			{Name: "a", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
			{Name: "b", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
			{Name: "c", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "CURRENT_TIMESTAMP"},
			{Name: "d", Type: "text", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: " it's "},
			{Name: "e", Type: "timestamp", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: " now() "},
			{Name: "f", Type: "int", Nullable: true, DefaultValue: "42"},
			{Name: "g", Type: "text", Nullable: true, DefaultValue: "CURRENT_TIMESTAMP"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE <db>.<t> (\n" +
		"  <id> int AUTO_INC NOT NULL,\n" +
		"  <a> text,\n" +
		"  <b> text DEFAULT NULL,\n" +
		"  <c> text DEFAULT 'CURRENT_TIMESTAMP',\n" +
		"  <d> text DEFAULT ' it''s ' NOT NULL,\n" +
		"  <e> timestamp DEFAULT now(),\n" +
		"  <f> int DEFAULT 42,\n" +
		"  <g> text DEFAULT CURRENT_TIMESTAMP\n)"}, statements)

	_, err = newDesignerSession(t).CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "text", AutoIncrement: true}},
	})
	require.ErrorContains(t, err, "cannot auto-increment", "方言不能让这一列自增时报它自己的错")
	_, err = newDesignerSession(t).CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "e", Type: "text", DefaultKind: dbdriver.DefaultKindExpression}},
	})
	require.Error(t, err, "写法不合法的请求被拒绝")
}

// 列定义要自增却没有方言的写法时报 ErrUnsupported，而不是悄悄丢掉自增。
func TestColumnDefinitionNeedsTheDialectForAutoIncrement(t *testing.T) {
	column := dbdriver.ColumnDefinition{Name: "id", Type: "int", AutoIncrement: true}
	_, err := ColumnDefinitionSQL(DefinitionLiterals, angle, column)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = NewColumnSQL(tablesDialect{}, DefinitionLiterals, angle, column)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	definition, err := NewColumnSQL(designerDialect{}, DefinitionLiterals, angle, column)
	require.NoError(t, err)
	assert.Equal(t, "<id> int AUTO_INC NOT NULL", definition)
}

// alterTable 是改表测试里读出的表：自增的 id，以及目录里写法各不相同的默认值。
func alterTable() *AlterableTable {
	return &AlterableTable{Name: "t", Columns: []AlterableColumn{
		{ColumnDefinition: dbdriver.ColumnDefinition{Name: "id", Type: "int"}},
		{ColumnDefinition: dbdriver.ColumnDefinition{Name: "name", Type: "text", DefaultValue: "'abc'::text"}},
		{ColumnDefinition: dbdriver.ColumnDefinition{Name: "at", Type: "timestamp", Nullable: true, DefaultValue: "now()"}},
		{ColumnDefinition: dbdriver.ColumnDefinition{Name: "note", Type: "text", Nullable: true, DefaultValue: "NULL"}},
	}}
}

// alterer 是测试里的改表：读出 alterTable（designer 时 id 自增），把改动逐列写成看得懂的语句，
// 新列经 NewColumnSQL、默认值经 DefaultSQL 写。reads 记下读表的次数。
type alterer struct {
	reads *int
	// seen 非 nil 时收到交给方言的改动。
	seen func(TableChange)
}

func (alterer) AlterAttributes() []dbdriver.AlterAttribute {
	return []dbdriver.AlterAttribute{dbdriver.AlterColumnDefault}
}

func (a alterer) read(autoIncrementID bool) *AlterableTable {
	*a.reads++
	table := alterTable()
	table.Columns[0].AutoIncrement = autoIncrementID
	return table
}

func (a alterer) write(dialect Dialect, change TableChange) ([]string, error) {
	if a.seen != nil {
		a.seen(change)
	}
	var statements []string
	for _, column := range change.Columns {
		if column.IsNew() {
			definition, err := NewColumnSQL(dialect, DefinitionLiterals, angle, column.Column)
			if err != nil {
				return nil, err
			}
			statements = append(statements, "ADD "+definition)
			continue
		}
		if column.DefaultChanged {
			literal, ok, err := DefaultSQL(DefinitionLiterals, column.Column)
			if err != nil {
				return nil, err
			}
			if !ok {
				statements = append(statements, "DROP DEFAULT "+column.Column.Name)
				continue
			}
			statements = append(statements, "SET DEFAULT "+column.Column.Name+" "+literal)
		}
	}
	return statements, nil
}

// plainAlterDialect 改表，但不开启任何列能力。
type plainAlterDialect struct {
	tablesDialect
	alterer
}

func (d plainAlterDialect) AlterableTable(context.Context, *sql.Conn, string, string) (*AlterableTable, error) {
	return d.read(false), nil
}

func (d plainAlterDialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change TableChange) ([]string, error) {
	return d.write(d, change)
}

// designerAlterDialect 改表，并开启两项列能力。
type designerAlterDialect struct {
	designerDialect
	alterer
}

func (d designerAlterDialect) AlterableTable(context.Context, *sql.Conn, string, string) (*AlterableTable, error) {
	return d.read(true), nil
}

func (d designerAlterDialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change TableChange) ([]string, error) {
	return d.write(d, change)
}

func newAlterSession(t *testing.T, dialect Dialect) *Session {
	t.Helper()
	db, _, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, dialect)
}

// modelRequest 把读出的表原样交回。
func modelRequest(model *dbdriver.AlterTableModel) dbdriver.AlterTableRequest {
	req := dbdriver.AlterTableRequest{Name: model.Name, Comment: model.Comment}
	for _, column := range model.Columns {
		req.Columns = append(req.Columns, dbdriver.AlterColumn{
			OriginalName: column.Name, Name: column.Name, Type: column.Type, Nullable: column.Nullable,
			DefaultValue: column.DefaultValue, Comment: column.Comment,
			DefaultKind: column.DefaultKind, AutoIncrement: column.AutoIncrement,
		})
	}
	return req
}

// 没开启列能力的方言：读出的列不带写法与自增；请求带了写法或要新列自增，在读表之前以 ErrUnsupported
// 拒绝；不带的请求照旧。
func TestAlterTableWithoutColumnFeatures(t *testing.T) {
	reads := 0
	session := newAlterSession(t, plainAlterDialect{alterer: alterer{reads: &reads}})
	ctx := context.Background()

	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)
	for _, column := range model.Columns {
		assert.Empty(t, column.DefaultKind, column.Name)
		assert.False(t, column.AutoIncrement, column.Name)
	}
	reads = 0

	withKind := modelRequest(model)
	withKind.Columns[1].DefaultKind = dbdriver.DefaultKindValue
	_, err = session.AlterTableStatements(ctx, "db", "t", withKind)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	newAutoIncrement := modelRequest(model)
	newAutoIncrement.Columns = append(newAutoIncrement.Columns, dbdriver.AlterColumn{Name: "seq", Type: "int", AutoIncrement: true})
	_, err = session.AlterTableStatements(ctx, "db", "t", newAutoIncrement)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Zero(t, reads, "拒绝在读表之前")

	legacy := modelRequest(model)
	legacy.Columns[1].DefaultValue = "x"
	result, err := session.AlterTableStatements(ctx, "db", "t", legacy)
	require.NoError(t, err)
	assert.Equal(t, []string{"SET DEFAULT name 'x'"}, result.Statements)
}

// 开启了列能力的方言：读出的列带方言判定的写法与自增现状，原样交回没有语句；改写法（值换成同样文本的
// 表达式）是改动；已有列的自增不能改；新列可以自增。
func TestAlterTableWithColumnFeatures(t *testing.T) {
	reads := 0
	session := newAlterSession(t, designerAlterDialect{alterer: alterer{reads: &reads}})
	ctx := context.Background()

	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ColumnDefinition{
		{Name: "id", Type: "int", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true},
		{Name: "name", Type: "text", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "abc"},
		{Name: "at", Type: "timestamp", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "now()"},
		{Name: "note", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
	}, model.Columns)

	result, err := session.AlterTableStatements(ctx, "db", "t", modelRequest(model))
	require.NoError(t, err)
	assert.Empty(t, result.Statements, "原样交回没有改动")

	req := modelRequest(model)
	req.Columns[1].DefaultKind = dbdriver.DefaultKindExpression // 同样的文本 abc，换成表达式
	req.Columns[2].DefaultKind, req.Columns[2].DefaultValue = dbdriver.DefaultKindValue, "now()"
	req.Columns[3].DefaultKind = dbdriver.DefaultKindNone
	req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "seq", Type: "int", AutoIncrement: true})
	result, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"SET DEFAULT name abc",
		"SET DEFAULT at 'now()'",
		"DROP DEFAULT note",
		"ADD <seq> int AUTO_INC NOT NULL",
	}, result.Statements)

	legacy := modelRequest(model)
	for i := range legacy.Columns {
		legacy.Columns[i].DefaultKind = ""
	}
	result, err = session.AlterTableStatements(ctx, "db", "t", legacy)
	require.NoError(t, err)
	assert.Empty(t, result.Statements, "不带写法的请求照旧比较文本")

	// 已有列不能开启自增。
	turnOn := modelRequest(model)
	turnOn.Columns[1].AutoIncrement = true
	_, err = session.AlterTableStatements(ctx, "db", "t", turnOn)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported, "已有列的自增不能改")
	assert.Contains(t, err.Error(), "auto-increment")

	// 不带自增（旧的改表对话框、没勾选的已有列）表示照旧：自增列不产生语句，改它的其他属性时方言
	// 拿到的仍是自增的现状，好在重写列定义时保留它。
	keep := modelRequest(model)
	keep.Columns[0].AutoIncrement = false
	result, err = session.AlterTableStatements(ctx, "db", "t", keep)
	require.NoError(t, err)
	assert.Empty(t, result.Statements, "不带自增的请求不改自增")

	reads = 0
	keep.Columns[0].DefaultKind, keep.Columns[0].DefaultValue = dbdriver.DefaultKindValue, "1"
	var seen *dbdriver.ColumnDefinition
	recorder := designerAlterDialect{alterer: alterer{reads: &reads, seen: func(change TableChange) {
		seen = &change.Columns[0].Column
	}}}
	_, err = newAlterSession(t, recorder).AlterTableStatements(ctx, "db", "t", keep)
	require.NoError(t, err)
	require.NotNil(t, seen)
	assert.True(t, seen.AutoIncrement, "方言拿到的列定义保留自增现状")
}

// DefaultSQL 是 DEFAULT 后面的内容：按写法写，没给写法时照旧猜；没有默认值时 ok 为假。
func TestDefaultSQL(t *testing.T) {
	for name, tc := range map[string]struct {
		column dbdriver.ColumnDefinition
		want   string
		ok     bool
	}{
		"legacy empty":       {dbdriver.ColumnDefinition{Type: "text", DefaultValue: "  "}, "", false},
		"legacy guess":       {dbdriver.ColumnDefinition{Type: "text", DefaultValue: "now()"}, "now()", true},
		"legacy quoted":      {dbdriver.ColumnDefinition{Type: "text", DefaultValue: "abc"}, "'abc'", true},
		"none":               {dbdriver.ColumnDefinition{Type: "text", DefaultKind: dbdriver.DefaultKindNone}, "", false},
		"null":               {dbdriver.ColumnDefinition{Type: "text", DefaultKind: dbdriver.DefaultKindNull}, "NULL", true},
		"empty value":        {dbdriver.ColumnDefinition{Type: "text", DefaultKind: dbdriver.DefaultKindValue}, "''", true},
		"number value":       {dbdriver.ColumnDefinition{Type: "int", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "42"}, "'42'", true},
		"expression trimmed": {dbdriver.ColumnDefinition{Type: "text", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: " a || b "}, "a || b", true},
	} {
		got, ok, err := DefaultSQL(DefinitionLiterals, tc.column)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)
		assert.Equal(t, tc.ok, ok, name)
	}

	unicode := Literals{UnicodeText: func(columnType string) bool { return columnType == "nvarchar" }}
	got, _, err := DefaultSQL(unicode, dbdriver.ColumnDefinition{Type: "nvarchar(10)", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "中"})
	require.NoError(t, err)
	assert.Equal(t, "N'中'", got, "值按列类型（括号前的类型名）由方言的字面量规则写")

	_, _, err = DefaultSQL(DefinitionLiterals, dbdriver.ColumnDefinition{DefaultKind: "function"})
	require.Error(t, err)
	assert.False(t, errors.Is(err, dbdriver.ErrUnsupported))
}
