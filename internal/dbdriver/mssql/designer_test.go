package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// indexRow 是 indexesSQL 的一行（索引的一列）。零值是一个启用的、存在默认文件组上、选项都是默认值的非聚集
// rowstore 索引的升序键列。
type indexRow struct {
	name                                 string
	indexType                            int // sys.indexes.type：1 聚集、2 非聚集，其余见 indexesSQL；0 按 2
	typeDesc                             string
	primary, uniqueConstraint, unique    bool
	filter                               string
	disabled, hypothetical, ignoreDupKey bool
	fillFactor                           int
	padded, rowLocksOff, pageLocksOff    bool
	noRecompute                          bool
	compression                          int
	dataSpaceType, dataSpaceName         string
	notDefaultDataSpace, memoryOptimized bool
	column                               string
	keyOrdinal                           int
	included, descending                 bool
}

// keyRow 是非聚集普通索引的第 ordinal 个升序键列。
func keyRow(name, column string, ordinal int) indexRow {
	return indexRow{name: name, column: column, keyOrdinal: ordinal}
}

// primaryKeyRow 是聚集主键约束的第 ordinal 个键列。
func primaryKeyRow(name, column string, ordinal int) indexRow {
	return indexRow{name: name, indexType: 1, primary: true, unique: true, column: column, keyOrdinal: ordinal}
}

func expectIndexes(mock sqlmock.Sqlmock, schema, table string, parts ...indexRow) {
	rows := sqlmock.NewRows([]string{
		"index_name", "index_type", "type_desc", "is_primary", "is_unique_constraint", "is_unique", "has_filter",
		"filter_definition", "is_disabled", "is_hypothetical", "ignore_dup_key", "fill_factor", "is_padded",
		"allow_row_locks", "allow_page_locks", "no_recompute", "data_compression", "data_space_type",
		"data_space_name", "data_space_default", "memory_optimized", "column_name", "key_ordinal", "is_included",
		"is_descending",
	})
	for _, p := range parts {
		indexType, typeDesc := p.indexType, p.typeDesc
		if indexType == 0 {
			indexType = 2
		}
		if typeDesc == "" {
			typeDesc = map[int]string{1: "CLUSTERED", 2: "NONCLUSTERED"}[indexType]
		}
		dataSpaceType, dataSpaceName := p.dataSpaceType, p.dataSpaceName
		if dataSpaceType == "" {
			dataSpaceType, dataSpaceName = "FG", "PRIMARY"
		}
		rows.AddRow(p.name, int64(indexType), typeDesc, p.primary, p.uniqueConstraint, p.unique, p.filter != "",
			p.filter, p.disabled, p.hypothetical, p.ignoreDupKey, int64(p.fillFactor), p.padded,
			!p.rowLocksOff, !p.pageLocksOff, p.noRecompute, int64(p.compression), dataSpaceType,
			dataSpaceName, !p.notDefaultDataSpace, int64(boolInt(p.memoryOptimized)), p.column, int64(p.keyOrdinal), p.included,
			p.descending)
	}
	mock.ExpectQuery(indexesSQL).WithArgs(schema, table).WillReturnRows(rows)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// expectIndexConstraints 是改表写索引语句前查的约束：名字 → 种类（PK / UQ）。
func expectIndexConstraints(mock sqlmock.Sqlmock, schema, table string, constraints ...[2]string) {
	rows := sqlmock.NewRows([]string{"name", "type"})
	for _, c := range constraints {
		rows.AddRow(c[0], c[1])
	}
	mock.ExpectQuery(indexConstraintsSQL).WithArgs(schema, table).WillReturnRows(rows)
}

// expectDesignedTable 读 dbo.users：列（带 IDENTITY 现状）与索引。
func expectDesignedTable(mock sqlmock.Sqlmock, columns []mssqlColumn, indexes ...indexRow) {
	expectColumnRows(mock, "dbo", "users", columns...)
	expectIndexes(mock, "dbo", "users", indexes...)
}

var usersColumns = []mssqlColumn{
	{name: "id", dataType: "bigint", nullable: "NO", identity: true},
	{name: "tenant_id", dataType: "int", nullable: "NO"},
	{name: "name", dataType: "nvarchar", maxLen: int64(100), nullable: "YES", def: "(N'anon')"},
	{name: "email", dataType: "nvarchar", maxLen: int64(255), nullable: "YES"},
	{name: "created", dataType: "datetime2", timePrecision: int64(7), nullable: "NO", def: "(getdate())"},
	{name: "score", dataType: "int", nullable: "YES", def: "((0))"},
}

// usersIndexes 是 dbo.users 的索引，按 indexesSQL 的次序（主键在前，其余按名字；索引里键列按键序在前，
// INCLUDE 列在后）。
var usersIndexes = []indexRow{
	primaryKeyRow("PK_users", "id", 1),
	keyRow("IX_cover", "tenant_id", 1),
	{name: "IX_cover", column: "email", included: true},
	{name: "IX_filtered", column: "email", keyOrdinal: 1, filter: "([email] IS NOT NULL)"},
	{name: "IX_name", column: "name", keyOrdinal: 1, descending: true},
	keyRow("IX_name", "tenant_id", 2),
	{name: "UQ_email", uniqueConstraint: true, unique: true, column: "email", keyOrdinal: 1},
}

func clustered(on bool) map[string]any { return map[string]any{clusteredOption: on} }

func usersIndexDefinitions() []dbdriver.IndexDefinition {
	return []dbdriver.IndexDefinition{
		{Name: "PK_users", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: clustered(true)},
		{Name: "IX_cover", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "tenant_id"}}, Include: []string{"email"},
			Options: clustered(false), ReadOnlyReason: "It has INCLUDE columns (email), which the designer cannot recreate."},
		{Name: "IX_filtered", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "email"}}, Options: clustered(false),
			ReadOnlyReason: "It is a filtered index (WHERE ([email] IS NOT NULL)), which the designer cannot recreate."},
		{Name: "IX_name", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "name", Descending: true}, {Name: "tenant_id"}},
			Options: clustered(false)},
		{Name: "UQ_email", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "email"}}, Options: clustered(false)},
	}
}

func keepIndexes(indexes []dbdriver.IndexDefinition) []dbdriver.AlterIndex {
	out := make([]dbdriver.AlterIndex, len(indexes))
	for i, index := range indexes {
		out[i] = dbdriver.AlterIndex{OriginalName: index.Name, IndexDefinition: index}
	}
	return out
}

func keepColumns(columns []dbdriver.ColumnDefinition) []dbdriver.AlterColumn {
	out := make([]dbdriver.AlterColumn, len(columns))
	for i, c := range columns {
		out[i] = dbdriver.AlterColumn{
			OriginalName: c.Name, Name: c.Name, Type: c.Type, Nullable: c.Nullable, DefaultValue: c.DefaultValue,
			Comment: c.Comment, DefaultKind: c.DefaultKind,
		}
	}
	return out
}

// usersModel 读出 dbo.users 的改表模型。
func usersModel(t *testing.T) *dbdriver.AlterTableModel {
	t.Helper()
	session, mock := newMock(t)
	expectDesignedTable(mock, usersColumns, usersIndexes...)
	model, err := session.AlterTableModel(context.Background(), "appdb", "dbo.users")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	return model
}

// usersRequest 是把 dbo.users 原样交回的改表请求，各测试在上面改。
func usersRequest(t *testing.T) dbdriver.AlterTableRequest {
	model := usersModel(t)
	return dbdriver.AlterTableRequest{Name: model.Name, Columns: keepColumns(model.Columns), Indexes: keepIndexes(model.Indexes)}
}

// alterUsers 生成改表语句：先读表，constraints 非 nil 时再查约束。
func alterUsers(t *testing.T, req dbdriver.AlterTableRequest, constraints [][2]string) ([]string, error) {
	t.Helper()
	session, mock := newMock(t)
	expectDesignedTable(mock, usersColumns, usersIndexes...)
	if constraints != nil {
		expectIndexConstraints(mock, "dbo", "users", constraints...)
	}
	result, err := session.AlterTableStatements(context.Background(), "appdb", "dbo.users", req)
	if err != nil {
		return nil, err
	}
	require.NoError(t, mock.ExpectationsWereMet())
	return result.Statements, nil
}

var usersConstraints = [][2]string{{"PK_users", "PK"}, {"UQ_email", "UQ"}}

// 改表模型：列的默认值按写法判定（数字与字符串常量是值，函数是表达式），IDENTITY 列报告自增；索引带类型、
// 键列（按键序，带降序）、INCLUDE 列与聚集选项，设计器写不回原样的带只读原因。索引名在表内唯一，不读别的表的。
func TestAlterTableModelReadsColumnsAndIndexes(t *testing.T) {
	model := usersModel(t)
	assert.Equal(t, []dbdriver.ColumnDefinition{
		{Name: "id", Type: "bigint", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true},
		{Name: "tenant_id", Type: "int", DefaultKind: dbdriver.DefaultKindNone},
		{Name: "name", Type: "nvarchar(100)", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "anon"},
		{Name: "email", Type: "nvarchar(255)", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
		{Name: "created", Type: "datetime2(7)", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "getdate()"},
		{Name: "score", Type: "int", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "0"},
	}, model.Columns)
	assert.Equal(t, usersIndexDefinitions(), model.Indexes)
	assert.Empty(t, model.TakenIndexNames)
}

// 原样交回读出的表（列与索引，包括只读的）不产生语句，也不查约束。
func TestAlterTableRoundTripGeneratesNothing(t *testing.T) {
	statements, err := alterUsers(t, usersRequest(t), nil)
	require.NoError(t, err)
	assert.Empty(t, statements)
}

// sys.default_constraints 的定义外面总有一层括号（数字两层）：数字、字符串常量（N 前缀、成对的单引号）是值，NULL
// 是 NULL，函数、运算、转换与十六进制常量是表达式（去掉外层括号，原样写回）；没有默认值是无。
func TestClassifyDefault(t *testing.T) {
	for definition, want := range map[string]struct {
		kind  dbdriver.DefaultKind
		value string
	}{
		"((0))":                {dbdriver.DefaultKindValue, "0"},
		"((-1))":               {dbdriver.DefaultKindValue, "-1"},
		"((1.5))":              {dbdriver.DefaultKindValue, "1.5"},
		"(N'abc')":             {dbdriver.DefaultKindValue, "abc"},
		"('abc')":              {dbdriver.DefaultKindValue, "abc"},
		"('it''s (x)')":        {dbdriver.DefaultKindValue, "it's (x)"},
		"(N'')":                {dbdriver.DefaultKindValue, ""},
		"(NULL)":               {dbdriver.DefaultKindNull, ""},
		"(getdate())":          {dbdriver.DefaultKindExpression, "getdate()"},
		"(newid())":            {dbdriver.DefaultKindExpression, "newid()"},
		"((0)+(1))":            {dbdriver.DefaultKindExpression, "(0)+(1)"},
		"('a'+'b')":            {dbdriver.DefaultKindExpression, "'a'+'b'"},
		"(CONVERT([bit],(0)))": {dbdriver.DefaultKindExpression, "CONVERT([bit],(0))"},
		"(0x01)":               {dbdriver.DefaultKindExpression, "0x01"},
	} {
		kind, value := dialect{}.ClassifyDefault(sqldb.AlterableColumn{
			ColumnDefinition: dbdriver.ColumnDefinition{DefaultValue: definition},
			Row:              map[string]any{"COLUMN_DEFAULT": definition},
		})
		assert.Equal(t, want.kind, kind, definition)
		assert.Equal(t, want.value, value, definition)
	}
	kind, value := dialect{}.ClassifyDefault(sqldb.AlterableColumn{Row: map[string]any{"COLUMN_DEFAULT": nil}})
	assert.Equal(t, dbdriver.DefaultKindNone, kind)
	assert.Empty(t, value)
}

// 新索引：降序列与聚集开关（CREATE CLUSTERED INDEX）；没给聚集选项的照 SQL Server 的缺省（非聚集）不写。
func TestAlterTableAddsIndexes(t *testing.T) {
	req := usersRequest(t)
	// 表上已有聚集主键：先把它改成非聚集，才能新建聚集索引。
	req.Indexes[0].Options = clustered(false)
	req.Indexes = append(req.Indexes,
		dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{Name: "IX_tenant", Type: dbdriver.IndexTypeNormal,
			Columns: []dbdriver.IndexColumn{{Name: "tenant_id"}}}},
		dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{Name: "IX_created", Type: dbdriver.IndexTypeUnique,
			Columns: []dbdriver.IndexColumn{{Name: "created", Descending: true}, {Name: "id"}}, Options: clustered(true)}},
	)
	statements, err := alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE [dbo].[users] DROP CONSTRAINT [PK_users]",
		// 聚集索引先建：之后建的主键与非聚集索引不必再随它重建。
		"CREATE UNIQUE CLUSTERED INDEX [IX_created] ON [dbo].[users] ([created] DESC, [id])",
		"ALTER TABLE [dbo].[users] ADD CONSTRAINT [PK_users] PRIMARY KEY NONCLUSTERED ([id])",
		"CREATE INDEX [IX_tenant] ON [dbo].[users] ([tenant_id])",
	}, statements)
}

// 改一个已有的普通索引：DROP INDEX … ON 表，再按改完的样子重建（读出的非聚集选项照写）。
func TestAlterTableRebuildsAChangedIndex(t *testing.T) {
	req := usersRequest(t)
	req.Indexes[3].Columns = []dbdriver.IndexColumn{{Name: "name"}, {Name: "email", Descending: true}}
	statements, err := alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX [IX_name] ON [dbo].[users]",
		"CREATE NONCLUSTERED INDEX [IX_name] ON [dbo].[users] ([name], [email] DESC)",
	}, statements)
}

// 改主键：随约束删（DROP CONSTRAINT），再 ADD CONSTRAINT … PRIMARY KEY，聚集与否照选项。
func TestAlterTableChangesThePrimaryKey(t *testing.T) {
	req := usersRequest(t)
	req.Indexes[0].Columns = []dbdriver.IndexColumn{{Name: "tenant_id"}, {Name: "id", Descending: true}}
	statements, err := alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE [dbo].[users] DROP CONSTRAINT [PK_users]",
		"ALTER TABLE [dbo].[users] ADD CONSTRAINT [PK_users] PRIMARY KEY CLUSTERED ([tenant_id], [id] DESC)",
	}, statements)
}

// 支撑 UNIQUE 约束的索引随约束删；重建时仍写成唯一约束，不悄悄变成唯一索引。
func TestAlterTableDropsAndRebuildsAUniqueConstraint(t *testing.T) {
	req := usersRequest(t)
	req.Indexes = req.Indexes[:4]
	req.IndexDrops = []string{"UQ_email"}
	statements, err := alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{"ALTER TABLE [dbo].[users] DROP CONSTRAINT [UQ_email]"}, statements)

	req = usersRequest(t)
	req.Indexes[4].Columns = []dbdriver.IndexColumn{{Name: "email"}, {Name: "tenant_id"}}
	statements, err = alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE [dbo].[users] DROP CONSTRAINT [UQ_email]",
		"ALTER TABLE [dbo].[users] ADD CONSTRAINT [UQ_email] UNIQUE NONCLUSTERED ([email], [tenant_id])",
	}, statements)
}

// 删列时引用它的索引——键列或只在 INCLUDE 里——先删（SQL Server 拒绝删被索引引用的列），写在 DROP COLUMN 之前。
func TestAlterTableDropsIndexesWithTheirColumn(t *testing.T) {
	req := usersRequest(t)
	var columns []dbdriver.AlterColumn
	for _, column := range req.Columns {
		if column.Name != "email" {
			columns = append(columns, column)
		}
	}
	req.Columns = columns
	req.Drops = []string{"email"}
	req.Indexes = []dbdriver.AlterIndex{req.Indexes[0], req.Indexes[3]}
	statements, err := alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX [IX_cover] ON [dbo].[users]",
		"DROP INDEX [IX_filtered] ON [dbo].[users]",
		"ALTER TABLE [dbo].[users] DROP CONSTRAINT [UQ_email]",
		"ALTER TABLE [dbo].[users] DROP COLUMN [email]",
	}, statements)
}

// 新列上的索引建在 ADD 之后；改名的表上索引语句用新表名；改名的列上的新索引用新列名。
func TestAlterTableCreatesIndexesAfterTheColumnChanges(t *testing.T) {
	req := usersRequest(t)
	req.Name = "members"
	req.Columns[1].Name = "org_id"
	req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "code", Type: "nvarchar(20)", Nullable: true,
		DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "无"})
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "IX_code", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "code"}, {Name: "tenant_id"}},
	}})
	statements, err := alterUsers(t, req, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"EXEC sp_rename 'dbo.users', 'members'",
		"ALTER TABLE [dbo].[members] ADD [code] nvarchar(20) DEFAULT N'无'",
		"EXEC sp_rename 'dbo.members.tenant_id', 'org_id', 'COLUMN'",
		"CREATE UNIQUE INDEX [IX_code] ON [dbo].[members] ([code], [org_id])",
	}, statements)
}

// 一张表只能有一个聚集索引：改完之后有两个打开聚集的索引时生成失败。
func TestAlterTableRefusesASecondClusteredIndex(t *testing.T) {
	req := usersRequest(t)
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "IX_tenant", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "tenant_id"}}, Options: clustered(true),
	}})
	_, err := alterUsers(t, req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clustered")
}

// 只读的索引（INCLUDE、筛选）不能改，只能删：改动以 ErrUnsupported 拒绝，不写任何语句；删除照常。
func TestAlterTableNeverRebuildsReadOnlyIndexes(t *testing.T) {
	for name, edit := range map[string]func(*dbdriver.AlterTableRequest){
		"include":  func(req *dbdriver.AlterTableRequest) { req.Indexes[1].Name = "IX_cover2" },
		"filtered": func(req *dbdriver.AlterTableRequest) { req.Indexes[2].Columns[0].Descending = true },
	} {
		t.Run(name, func(t *testing.T) {
			req := usersRequest(t)
			edit(&req)
			_, err := alterUsers(t, req, nil)
			require.ErrorIs(t, err, dbdriver.ErrUnsupported)
		})
	}

	req := usersRequest(t)
	req.Indexes = append(req.Indexes[:1], req.Indexes[2:]...)
	req.IndexDrops = []string{"IX_cover"}
	statements, err := alterUsers(t, req, usersConstraints)
	require.NoError(t, err)
	assert.Equal(t, []string{"DROP INDEX [IX_cover] ON [dbo].[users]"}, statements)
}

// 设计器表示不了的索引都只读，各自给出原因：列存储、XML / 空间 / 哈希等非 rowstore、禁用、假设索引、
// 内存优化表的、非默认的索引选项与压缩、分区与非默认文件组。
func TestAlterTableModelMarksIndexesTheDesignerCannotRecreate(t *testing.T) {
	for name, tc := range map[string]struct {
		row    indexRow
		reason string
	}{
		"columnstore":  {indexRow{indexType: 6, typeDesc: "NONCLUSTERED COLUMNSTORE", included: true}, "NONCLUSTERED COLUMNSTORE"},
		"xml":          {indexRow{indexType: 3, typeDesc: "XML", keyOrdinal: 1}, "XML"},
		"spatial":      {indexRow{indexType: 4, typeDesc: "SPATIAL", keyOrdinal: 1}, "SPATIAL"},
		"hash":         {indexRow{indexType: 7, typeDesc: "NONCLUSTERED HASH", keyOrdinal: 1, memoryOptimized: true}, "NONCLUSTERED HASH"},
		"disabled":     {indexRow{keyOrdinal: 1, disabled: true}, "disabled"},
		"hypothetical": {indexRow{keyOrdinal: 1, hypothetical: true}, "hypothetical"},
		"memory":       {indexRow{keyOrdinal: 1, memoryOptimized: true}, "memory-optimized"},
		"fill factor":  {indexRow{keyOrdinal: 1, fillFactor: 80}, "FILLFACTOR = 80"},
		"pad index":    {indexRow{keyOrdinal: 1, padded: true}, "PAD_INDEX = ON"},
		"ignore dup":   {indexRow{keyOrdinal: 1, unique: true, ignoreDupKey: true}, "IGNORE_DUP_KEY = ON"},
		"row locks":    {indexRow{keyOrdinal: 1, rowLocksOff: true}, "ALLOW_ROW_LOCKS = OFF"},
		"page locks":   {indexRow{keyOrdinal: 1, pageLocksOff: true}, "ALLOW_PAGE_LOCKS = OFF"},
		"norecompute":  {indexRow{keyOrdinal: 1, noRecompute: true}, "STATISTICS_NORECOMPUTE = ON"},
		"compression":  {indexRow{keyOrdinal: 1, compression: 2}, "DATA_COMPRESSION = PAGE"},
		"partitioned":  {indexRow{keyOrdinal: 1, dataSpaceType: "PS", dataSpaceName: "ps_month"}, "ps_month"},
		"filegroup":    {indexRow{keyOrdinal: 1, dataSpaceType: "FG", dataSpaceName: "FG_INDEX", notDefaultDataSpace: true}, "FG_INDEX"},
	} {
		t.Run(name, func(t *testing.T) {
			row := tc.row
			row.name, row.column = "IX_x", "email"
			session, mock := newMock(t)
			expectDesignedTable(mock, usersColumns, row)
			model, err := session.AlterTableModel(context.Background(), "appdb", "dbo.users")
			require.NoError(t, err)
			require.Len(t, model.Indexes, 1)
			assert.Contains(t, model.Indexes[0].ReadOnlyReason, tc.reason)
			assert.Equal(t, []dbdriver.IndexColumn{{Name: "email"}}, model.Indexes[0].Columns)
		})
	}

	// 聚集列存储占着这张表唯一的聚集位置：读出来聚集打开。
	session, mock := newMock(t)
	expectDesignedTable(mock, usersColumns, indexRow{name: "CCI", indexType: 5, typeDesc: "CLUSTERED COLUMNSTORE", column: "id", included: true})
	model, err := session.AlterTableModel(context.Background(), "appdb", "dbo.users")
	require.NoError(t, err)
	assert.Equal(t, clustered(true), model.Indexes[0].Options)
}

// 建表：IDENTITY(1,1) 自增主键写在表定义里（CONSTRAINT 名 PRIMARY KEY），按写法的默认值（值按列类型写字面量，
// nvarchar 带 N 前缀；表达式原样），其余索引在建表之后建、聚集的先建；没给聚集选项的主键在另有聚集索引时写成
// NONCLUSTERED（否则 SQL Server 把它建成聚集，之后的聚集索引建不成）。
func TestCreateTableWithIndexesAndIdentity(t *testing.T) {
	session, _ := newMock(t)
	statements, err := session.CreateTableStatements(context.Background(), "appdb", dbdriver.CreateTableRequest{
		Name: "dbo.orders",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "bigint", AutoIncrement: true, DefaultKind: dbdriver.DefaultKindNone},
			{Name: "title", Type: "nvarchar(255)", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "新'单"},
			{Name: "created", Type: "datetime2", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: " getdate() "},
			{Name: "note", Type: "varchar(50)", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
			{Name: "qty", Type: "int", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "0"},
		},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "PK_orders", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
			{Name: "IX_title", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "title", Descending: true}}},
			{Name: "IX_created", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "created"}}, Options: clustered(true)},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATE TABLE [dbo].[orders] (\n" +
			"  [id] bigint IDENTITY(1,1) NOT NULL,\n" +
			"  [title] nvarchar(255) DEFAULT N'新''单',\n" +
			"  [created] datetime2 DEFAULT getdate() NOT NULL,\n" +
			"  [note] varchar(50) DEFAULT NULL,\n" +
			"  [qty] int DEFAULT '0' NOT NULL,\n" +
			"  CONSTRAINT [PK_orders] PRIMARY KEY NONCLUSTERED ([id])\n" +
			")",
		"CREATE CLUSTERED INDEX [IX_created] ON [dbo].[orders] ([created])",
		"CREATE INDEX [IX_title] ON [dbo].[orders] ([title] DESC)",
	}, statements)

	// 没有别的聚集索引时主键照 SQL Server 的缺省（聚集）不写；建表请求里两个聚集索引生成失败。
	statements, err = session.CreateTableStatements(context.Background(), "appdb", dbdriver.CreateTableRequest{
		Name:    "t",
		Columns: []dbdriver.NewColumn{{Name: "id", Type: "int", AutoIncrement: true}},
		Indexes: []dbdriver.IndexDefinition{{Name: "", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE [t] (\n  [id] int IDENTITY(1,1) NOT NULL,\n  PRIMARY KEY ([id])\n)"}, statements)

	_, err = session.CreateTableStatements(context.Background(), "appdb", dbdriver.CreateTableRequest{
		Name:    "t",
		Columns: []dbdriver.NewColumn{{Name: "id", Type: "int"}, {Name: "a", Type: "int"}},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "PK_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: clustered(true)},
			{Name: "IX_a", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "a"}}, Options: clustered(true)},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clustered")
}

// 只有整数类型（与小数位为 0 的 decimal / numeric）能作 IDENTITY；IDENTITY 列不能再有默认值；建表时的注释照旧不支持。
func TestCreateTableRefusesWhatSQLServerCannotWrite(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()
	_, err := session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "nvarchar(10)", AutoIncrement: true}},
	})
	assert.ErrorContains(t, err, "integer")
	_, err = session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "int", AutoIncrement: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "1"}},
	})
	assert.ErrorContains(t, err, "default")
	statements, err := session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "numeric(10, 0)", AutoIncrement: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE [t] (\n  [id] numeric(10, 0) IDENTITY(1,1) NOT NULL\n)"}, statements)

	_, err = session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "t", Comment: "x", Columns: []dbdriver.NewColumn{{Name: "id", Type: "int"}},
	})
	assert.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "int", Comment: "x"}},
	})
	assert.ErrorIs(t, err, dbdriver.ErrUnsupported)
}

// 新增的 IDENTITY 列写在 ADD 里；已有列不能开启自增。
func TestAlterTableAddsAnIdentityColumn(t *testing.T) {
	session, mock := newMock(t)
	expectAlterColumns(mock, "dbo", "logs", mssqlColumn{name: "msg", dataType: "nvarchar", maxLen: int64(-1), nullable: "YES"})
	result, err := session.AlterTableStatements(context.Background(), "appdb", "dbo.logs", dbdriver.AlterTableRequest{
		Name: "logs",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "msg", Name: "msg", Type: "nvarchar(max)", Nullable: true},
			{Name: "id", Type: "bigint", AutoIncrement: true},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ALTER TABLE [dbo].[logs] ADD [id] bigint IDENTITY(1,1) NOT NULL"}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())

	session, mock = newMock(t)
	expectAlterColumns(mock, "dbo", "logs", mssqlColumn{name: "n", dataType: "int", nullable: "NO"})
	_, err = session.AlterTableStatements(context.Background(), "appdb", "dbo.logs", dbdriver.AlterTableRequest{
		Name: "logs", Columns: []dbdriver.AlterColumn{{OriginalName: "n", Name: "n", Type: "int", AutoIncrement: true}},
	})
	assert.ErrorIs(t, err, dbdriver.ErrUnsupported)
}

// 外键读出 ON UPDATE / ON DELETE：sys.foreign_keys 的 *_referential_action_desc（NO_ACTION、SET_NULL…）换成
// 带空格的写法；多列外键按列的次序。
func TestTableMetadataReadsForeignKeyActions(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(columnsSQL).WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT"}).
		AddRow("user_id", "int", "YES", nil))
	mock.ExpectQuery(primaryKeysSQL).WillReturnRows(names("COLUMN_NAME"))
	expectIndexes(mock, "dbo", "orders")
	mock.ExpectQuery(foreignKeysSQL).WithArgs("dbo", "orders").WillReturnRows(
		sqlmock.NewRows([]string{"constraint_name", "column_name", "referenced_table", "referenced_column", "on_update", "on_delete"}).
			AddRow("FK_user", "user_id", "users", "id", "NO ACTION", "CASCADE").
			AddRow("FK_item", "item_tenant", "items", "tenant", "SET NULL", "SET DEFAULT"))

	meta, err := session.TableMetadata(context.Background(), "appdb", "dbo.orders")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{
		{Name: "FK_user", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id", OnUpdate: "NO ACTION", OnDelete: "CASCADE"},
		{Name: "FK_item", Column: "item_tenant", ReferencedTable: "items", ReferencedColumn: "tenant", OnUpdate: "SET NULL", OnDelete: "SET DEFAULT"},
	}, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}
