package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ddlColumnRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "type_name", "max_length", "precision", "scale", "is_nullable",
		"identity_seed", "identity_increment", "default_definition", "default_name", "computed_definition", "is_persisted"})
}

func ddlIndexRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "is_primary_key", "is_unique_constraint", "is_unique", "type",
		"filter_definition", "column_name", "is_descending_key", "is_included_column"})
}

func ddlCheckRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "definition"})
}

// 一张覆盖各类元素的表：类型长度（nvarchar 按字符数、max）、精度与小数位、IDENTITY(种子, 增量)、
// 默认值、主键 / 唯一约束 / CHECK 写在 CREATE TABLE 里，普通索引（含降序、INCLUDE、过滤条件）单独成句。
func TestTableDDLRebuildsRestorableStatements(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("[sales].[orders]").WillReturnRows(ddlColumnRows().
		AddRow("id", "int", int64(4), int64(10), int64(0), false, int64(100), int64(5), nil, nil, nil, false).
		AddRow("name", "nvarchar", int64(100), int64(0), int64(0), true, nil, nil, nil, nil, nil, false).
		AddRow("bio", "nvarchar", int64(-1), int64(0), int64(0), true, nil, nil, nil, nil, nil, false).
		AddRow("code", "char", int64(8), int64(0), int64(0), false, nil, nil, "('N/A')", nil, nil, false).
		AddRow("raw", "varbinary", int64(-1), int64(0), int64(0), true, nil, nil, nil, nil, nil, false).
		AddRow("price", "decimal", int64(9), int64(18), int64(4), false, nil, nil, "((0))", nil, nil, false).
		AddRow("at", "datetime2", int64(8), int64(27), int64(3), true, nil, nil, nil, nil, nil, false).
		AddRow("ratio", "float", int64(8), int64(53), int64(0), true, nil, nil, nil, nil, nil, false).
		AddRow("small", "float", int64(4), int64(24), int64(0), true, nil, nil, nil, nil, nil, false))
	mock.ExpectQuery(ddlIndexesQuery).WithArgs("[sales].[orders]").WillReturnRows(ddlIndexRows().
		AddRow("PK_orders", true, false, true, int64(1), nil, "id", false, false).
		AddRow("UQ_orders_code", false, true, true, int64(2), nil, "code", false, false).
		AddRow("UQ_orders_code", false, true, true, int64(2), nil, "name", true, false).
		AddRow("IX_orders_at", false, false, true, int64(2), "([at] IS NOT NULL)", "at", true, false).
		AddRow("IX_orders_at", false, false, true, int64(2), "([at] IS NOT NULL)", "price", false, true))
	mock.ExpectQuery(ddlChecksQuery).WithArgs("[sales].[orders]").WillReturnRows(ddlCheckRows().
		AddRow("CK_orders_price", "([price]>=(0))"))

	ddl, err := session.TableDDL(context.Background(), "appdb", "sales.orders")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE [sales].[orders] (\n"+
		"  [id] int IDENTITY(100, 5) NOT NULL,\n"+
		"  [name] nvarchar(50) NULL,\n"+
		"  [bio] nvarchar(max) NULL,\n"+
		"  [code] char(8) NOT NULL DEFAULT ('N/A'),\n"+
		"  [raw] varbinary(max) NULL,\n"+
		"  [price] decimal(18, 4) NOT NULL DEFAULT ((0)),\n"+
		"  [at] datetime2(3) NULL,\n"+
		"  [ratio] float NULL,\n"+
		"  [small] float(24) NULL,\n"+
		"  CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id]),\n"+
		"  CONSTRAINT [UQ_orders_code] UNIQUE NONCLUSTERED ([code], [name] DESC),\n"+
		"  CONSTRAINT [CK_orders_price] CHECK ([price]>=(0))\n"+
		");\n"+
		"CREATE UNIQUE NONCLUSTERED INDEX [IX_orders_at] ON [sales].[orders] ([at] DESC) INCLUDE ([price]) WHERE ([at] IS NOT NULL);", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 裸表名按 dbo 查询，语句里的表名保持原样（不补 schema）；没有约束与索引时只有建表语句。
func TestTableDDLOfABareNameQueriesDbo(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("[dbo].[users]").WillReturnRows(ddlColumnRows().
		AddRow("id", "int", int64(4), int64(10), int64(0), false, nil, nil, nil, nil, nil, false))
	mock.ExpectQuery(ddlIndexesQuery).WithArgs("[dbo].[users]").WillReturnRows(ddlIndexRows())
	mock.ExpectQuery(ddlChecksQuery).WithArgs("[dbo].[users]").WillReturnRows(ddlCheckRows())

	ddl, err := session.TableDDL(context.Background(), "appdb", "users")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE [users] (\n  [id] int NOT NULL\n);", ddl)
}

// 计算列写成 AS (表达式)，持久化的带 PERSISTED（不可为空时再带 NOT NULL；非持久化的计算列不能写可空性）；
// 不是系统起名的默认值约束带上它的名字，系统起名的不写名字（还原时再由系统起名）。
func TestTableDDLWritesComputedColumnsAndNamedDefaults(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("[dbo].[lines]").WillReturnRows(ddlColumnRows().
		AddRow("qty", "int", int64(4), int64(10), int64(0), false, nil, nil, "((1))", "DF_lines_qty", nil, false).
		AddRow("price", "decimal", int64(9), int64(18), int64(4), false, nil, nil, "((0))", nil, nil, false).
		AddRow("total", "decimal", int64(13), int64(29), int64(4), false, nil, nil, nil, nil, "([price]*[qty])", true).
		AddRow("maybe", "int", int64(4), int64(10), int64(0), true, nil, nil, nil, nil, "([qty]/nullif([qty],(0)))", true).
		AddRow("label", "nvarchar", int64(62), int64(0), int64(0), true, nil, nil, nil, nil, "(N'#'+CONVERT([nvarchar](30),[qty]))", false).
		AddRow("rv", "timestamp", int64(8), int64(0), int64(0), false, nil, nil, nil, nil, nil, false))
	mock.ExpectQuery(ddlIndexesQuery).WithArgs("[dbo].[lines]").WillReturnRows(ddlIndexRows())
	mock.ExpectQuery(ddlChecksQuery).WithArgs("[dbo].[lines]").WillReturnRows(ddlCheckRows())

	ddl, err := session.TableDDL(context.Background(), "appdb", "dbo.lines")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE [dbo].[lines] (\n"+
		"  [qty] int NOT NULL CONSTRAINT [DF_lines_qty] DEFAULT ((1)),\n"+
		"  [price] decimal(18, 4) NOT NULL DEFAULT ((0)),\n"+
		"  [total] AS ([price]*[qty]) PERSISTED NOT NULL,\n"+
		"  [maybe] AS ([qty]/nullif([qty],(0))) PERSISTED,\n"+
		"  [label] AS (N'#'+CONVERT([nvarchar](30),[qty])),\n"+
		"  [rv] timestamp NOT NULL\n"+
		");", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTableDDLOfAMissingTableIsEmpty(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("[dbo].[gone]").WillReturnRows(ddlColumnRows())

	ddl, err := session.TableDDL(context.Background(), "appdb", "gone")
	require.NoError(t, err)
	assert.Empty(t, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStarterQueryUsesTop(t *testing.T) {
	session, _ := newMock(t)
	got, err := session.StarterQuery(context.Background(), "appdb", "dbo.users", 100)
	require.NoError(t, err)
	assert.Equal(t, "SELECT TOP 100 * FROM [dbo].[users]", got)
}
