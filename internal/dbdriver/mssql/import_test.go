package mssql

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// importColumn 是导入测试里表的一列：名称、类型，以及是否是 IDENTITY 列。
type importColumn struct {
	name, typ string
	identity  bool
}

func expectImportColumns(mock sqlmock.Sqlmock, schema, table string, columns ...importColumn) {
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS"})
	for _, c := range columns {
		flag := 0
		if c.identity {
			flag = 1
		}
		rows.AddRow(c.name, c.typ, "YES", nil, flag, flag)
	}
	mock.ExpectQuery(columnsSQL).WithArgs(schema, table).WillReturnRows(rows)
}

// expectViewColumns 是 columnsSQL 对一个视图的回答：视图的 IDENTITY 列在 COLUMNPROPERTY 里同样
// 报告为 IsIdentity，OBJECT_ID(…, N'V') 也找得到它。
func expectViewColumns(mock sqlmock.Sqlmock, schema, view string, columns ...importColumn) {
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS", "IS_NO_IDENTITY_OVERRIDE"})
	for _, c := range columns {
		flag := 0
		if c.identity {
			flag = 1
		}
		rows.AddRow(c.name, c.typ, "YES", nil, flag, flag, flag)
	}
	mock.ExpectQuery(columnsSQL).WithArgs(schema, view).WillReturnRows(rows)
}

func texts(values ...string) []dbdriver.ImportValue {
	out := make([]dbdriver.ImportValue, len(values))
	for i, v := range values {
		out[i] = dbdriver.ImportValue{Kind: dbdriver.ImportText, Text: v}
	}
	return out
}

// users 是没有自增列的表（id 是普通主键）。
var plainUsers = []importColumn{{"id", "int", false}, {"name", "varchar", false}, {"email", "varchar", false}}

func importUsers(mode dbdriver.ImportMode, rows ...[]dbdriver.ImportValue) dbdriver.ImportRequest {
	return dbdriver.ImportRequest{Mode: mode, Columns: []string{"id", "name", "email"}, Keys: []string{"id"}, Rows: rows}
}

const identityOn = "SET IDENTITY_INSERT [dbo].[users] ON; BEGIN TRY "
const identityOff = " END TRY BEGIN CATCH SET IDENTITY_INSERT [dbo].[users] OFF; THROW; END CATCH; SET IDENTITY_INSERT [dbo].[users] OFF;"

// 映射里没有自增列：各模式的语句不碰 IDENTITY_INSERT，也不再套运行时查 sys.identity_columns 的判断；
// 其余与改动前前端生成的文本逐字一致（原 tableImport.test.ts 的断言）。
func TestImportWithoutIdentityColumnHasNoIdentityWrapperForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	row := texts("1", "Alice", "alice@example.test")
	generate := func(mode dbdriver.ImportMode) []string {
		t.Helper()
		expectImportColumns(mock, "dbo", "users", plainUsers...)
		statements, err := session.ImportStatements(ctx, "appdb", "dbo.users", importUsers(mode, row))
		require.NoError(t, err)
		return statements
	}

	insert := "INSERT INTO [dbo].[users] ([id], [name], [email]) VALUES ('1', 'Alice', 'alice@example.test');"
	assert.Equal(t, []string{insert}, generate(dbdriver.ImportAppend))
	assert.Equal(t, []string{"DELETE FROM [dbo].[users];", insert}, generate(dbdriver.ImportCopy))
	assert.Equal(t, []string{
		"MERGE [dbo].[users] AS t USING (SELECT '1' AS [id], 'Alice' AS [name], 'alice@example.test' AS [email]) AS s " +
			"ON (t.[id] = s.[id]) " +
			"WHEN MATCHED THEN UPDATE SET t.[name] = s.[name], t.[email] = s.[email] " +
			"WHEN NOT MATCHED THEN INSERT ([id], [name], [email]) VALUES (s.[id], s.[name], s.[email]);",
	}, generate(dbdriver.ImportAppendUpdate))
	assert.Equal(t, []string{
		"MERGE [dbo].[users] AS t USING (SELECT '1' AS [id], 'Alice' AS [name], 'alice@example.test' AS [email]) AS s " +
			"ON (t.[id] = s.[id]) " +
			"WHEN NOT MATCHED THEN INSERT ([id], [name], [email]) VALUES (s.[id], s.[name], s.[email]);",
	}, generate(dbdriver.ImportAppendSkip))
	assert.Equal(t, []string{
		"UPDATE [dbo].[users] SET [name] = 'Alice', [email] = 'alice@example.test' WHERE [id] = '1';",
	}, generate(dbdriver.ImportUpdate))
	assert.Equal(t, []string{"DELETE FROM [dbo].[users] WHERE [id] = '1';"}, generate(dbdriver.ImportDelete))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 映射包含 IDENTITY 列：插入类的语句临时打开这张表的 IDENTITY_INSERT、写完关闭，出错也关闭并原样
// 抛出；复制模式的清空语句不包。多行 INSERT 整条一起包。
func TestImportWritesMappedIdentityValuesForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	identityUsers := []importColumn{{"id", "int", true}, {"name", "varchar", false}, {"email", "varchar", false}}
	generate := func(req dbdriver.ImportRequest) []string {
		t.Helper()
		expectImportColumns(mock, "dbo", "users", identityUsers...)
		statements, err := session.ImportStatements(ctx, "appdb", "dbo.users", req)
		require.NoError(t, err)
		return statements
	}

	insert := "INSERT INTO [dbo].[users] ([id], [name], [email]) VALUES ('1', 'A', 'a@x');"
	assert.Equal(t, []string{identityOn + insert + identityOff}, generate(importUsers(dbdriver.ImportAppend, texts("1", "A", "a@x"))))
	assert.Equal(t, []string{"DELETE FROM [dbo].[users];", identityOn + insert + identityOff},
		generate(importUsers(dbdriver.ImportCopy, texts("1", "A", "a@x"))))

	extended := importUsers(dbdriver.ImportAppend, texts("1", "A", "a@x"), texts("2", "B", "b@x"))
	extended.ExtendedInsert, extended.MaxStatementSizeKB = true, 1
	assert.Equal(t, []string{
		identityOn + "INSERT INTO [dbo].[users] ([id], [name], [email]) VALUES ('1', 'A', 'a@x'), ('2', 'B', 'b@x');" + identityOff,
	}, generate(extended))

	// 只映射了非自增列：不碰 IDENTITY_INSERT。
	assert.Equal(t, []string{"INSERT INTO [dbo].[users] ([name]) VALUES ('A');"}, generate(dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"name"}, Rows: [][]dbdriver.ImportValue{texts("A")},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 追加/更新与更新不去改 IDENTITY 列：MERGE 的 UPDATE SET 与 UPDATE 的 SET 都不含它（已有行的其余列
// 照常更新，自增列保持原值），新插入的行仍写入源文件里的值（打开 IDENTITY_INSERT）。
func TestImportUpdateModesSkipTheIdentityColumnForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	items := []importColumn{{"code", "varchar", false}, {"id", "int", true}, {"name", "varchar", false}}
	generate := func(mode dbdriver.ImportMode, columns ...string) []string {
		t.Helper()
		expectImportColumns(mock, "dbo", "items", items...)
		values := map[string]string{"code": "A1", "id": "7", "name": "Alice"}
		row := make([]dbdriver.ImportValue, len(columns))
		for i, c := range columns {
			row[i] = dbdriver.ImportValue{Kind: dbdriver.ImportText, Text: values[c]}
		}
		statements, err := session.ImportStatements(ctx, "appdb", "dbo.items", dbdriver.ImportRequest{
			Mode: mode, Columns: columns, Keys: []string{"code"}, Rows: [][]dbdriver.ImportValue{row},
		})
		require.NoError(t, err)
		return statements
	}

	assert.Equal(t, []string{
		"SET IDENTITY_INSERT [dbo].[items] ON; BEGIN TRY " +
			"MERGE [dbo].[items] AS t USING (SELECT 'A1' AS [code], '7' AS [id], 'Alice' AS [name]) AS s " +
			"ON (t.[code] = s.[code]) " +
			"WHEN MATCHED THEN UPDATE SET t.[name] = s.[name] " +
			"WHEN NOT MATCHED THEN INSERT ([code], [id], [name]) VALUES (s.[code], s.[id], s.[name]);" +
			" END TRY BEGIN CATCH SET IDENTITY_INSERT [dbo].[items] OFF; THROW; END CATCH; SET IDENTITY_INSERT [dbo].[items] OFF;",
	}, generate(dbdriver.ImportAppendUpdate, "code", "id", "name"))
	assert.Equal(t, []string{"UPDATE [dbo].[items] SET [name] = 'Alice' WHERE [code] = 'A1';"},
		generate(dbdriver.ImportUpdate, "code", "id", "name"))

	// 除了键只映射了自增列：追加/更新退化成只插入不更新，更新模式没有可改的列。
	assert.Equal(t, []string{
		"SET IDENTITY_INSERT [dbo].[items] ON; BEGIN TRY " +
			"MERGE [dbo].[items] AS t USING (SELECT 'A1' AS [code], '7' AS [id]) AS s " +
			"ON (t.[code] = s.[code]) " +
			"WHEN NOT MATCHED THEN INSERT ([code], [id]) VALUES (s.[code], s.[id]);" +
			" END TRY BEGIN CATCH SET IDENTITY_INSERT [dbo].[items] OFF; THROW; END CATCH; SET IDENTITY_INSERT [dbo].[items] OFF;",
	}, generate(dbdriver.ImportAppendUpdate, "code", "id"))
	assert.Empty(t, generate(dbdriver.ImportUpdate, "code", "id"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 目标是视图（映射含视图里的 IDENTITY 列）：插入类语句不开关 IDENTITY_INSERT（对视图开关报 8105、
// 盖住真实原因），按普通插入生成；更新类语句与用户表一样不去改 IDENTITY 列。
func TestImportIntoAViewWithAnIdentityColumnForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	items := []importColumn{{"code", "varchar", false}, {"id", "int", true}, {"name", "varchar", false}}
	generate := func(mode dbdriver.ImportMode) []string {
		t.Helper()
		expectViewColumns(mock, "dbo", "v_items", items...)
		statements, err := session.ImportStatements(ctx, "appdb", "dbo.v_items", dbdriver.ImportRequest{
			Mode: mode, Columns: []string{"code", "id", "name"}, Keys: []string{"code"},
			Rows: [][]dbdriver.ImportValue{texts("A1", "7", "Alice")},
		})
		require.NoError(t, err)
		return statements
	}

	assert.Equal(t, []string{"INSERT INTO [dbo].[v_items] ([code], [id], [name]) VALUES ('A1', '7', 'Alice');"},
		generate(dbdriver.ImportAppend))
	assert.Equal(t, []string{
		"MERGE [dbo].[v_items] AS t USING (SELECT 'A1' AS [code], '7' AS [id], 'Alice' AS [name]) AS s " +
			"ON (t.[code] = s.[code]) " +
			"WHEN MATCHED THEN UPDATE SET t.[name] = s.[name] " +
			"WHEN NOT MATCHED THEN INSERT ([code], [id], [name]) VALUES (s.[code], s.[id], s.[name]);",
	}, generate(dbdriver.ImportAppendUpdate))
	assert.Equal(t, []string{"UPDATE [dbo].[v_items] SET [name] = 'Alice' WHERE [code] = 'A1';"},
		generate(dbdriver.ImportUpdate))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 字面量按目标列：nvarchar 文本带 N 前缀，varbinary 写成 0x…，NULL 照旧；按 NULL 的键定位写 IS NULL。
// 对象名与列名里的引号照方括号规则转义。
func TestImportLiteralsForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	files := []importColumn{{"id", "int", false}, {"name", "nvarchar", false}, {"payload", "varbinary", false}}

	expectImportColumns(mock, "dbo", "files", files...)
	statements, err := session.ImportStatements(ctx, "appdb", "dbo.files", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"id", "name", "payload"},
		Rows: [][]dbdriver.ImportValue{
			{{Kind: dbdriver.ImportText, Text: "1"}, {Kind: dbdriver.ImportText, Text: "中文"}, {Kind: dbdriver.ImportBinary, Hex: "48656c6c6f"}},
			{{Kind: dbdriver.ImportText, Text: "2"}, {Kind: dbdriver.ImportNull}, {Kind: dbdriver.ImportBinary, Hex: ""}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"INSERT INTO [dbo].[files] ([id], [name], [payload]) VALUES ('1', N'中文', 0x48656c6c6f);",
		"INSERT INTO [dbo].[files] ([id], [name], [payload]) VALUES ('2', NULL, 0x);",
	}, statements)

	expectImportColumns(mock, "dbo", "files", files...)
	statements, err = session.ImportStatements(ctx, "appdb", "dbo.files", dbdriver.ImportRequest{
		Mode: dbdriver.ImportDelete, Columns: []string{"name"}, Keys: []string{"name"},
		Rows: [][]dbdriver.ImportValue{{{Kind: dbdriver.ImportNull}}, {{Kind: dbdriver.ImportText, Text: "Ünï"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DELETE FROM [dbo].[files] WHERE [name] IS NULL;",
		"DELETE FROM [dbo].[files] WHERE [name] = N'Ünï';",
	}, statements)

	expectImportColumns(mock, "dbo", "o'brien", importColumn{"i]d", "int", true})
	statements, err = session.ImportStatements(ctx, "appdb", "dbo.o'brien", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"i]d"}, Rows: [][]dbdriver.ImportValue{texts("1")},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"SET IDENTITY_INSERT [dbo].[o'brien] ON; BEGIN TRY INSERT INTO [dbo].[o'brien] ([i]]d]) VALUES ('1'); END TRY " +
			"BEGIN CATCH SET IDENTITY_INSERT [dbo].[o'brien] OFF; THROW; END CATCH; SET IDENTITY_INSERT [dbo].[o'brien] OFF;",
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 导入摘要的「新增 / 更新」取自每条语句的受影响行数：包装里不能有变量赋值（SQL Server 把它计为
// 1 行），否则每条都多算 1 行。
func TestImportAddsNoRowCountOfItsOwnForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	for _, mode := range []dbdriver.ImportMode{dbdriver.ImportAppend, dbdriver.ImportAppendUpdate, dbdriver.ImportAppendSkip, dbdriver.ImportCopy} {
		expectImportColumns(mock, "dbo", "users", importColumn{"id", "int", true}, importColumn{"name", "varchar", false})
		statements, err := session.ImportStatements(context.Background(), "appdb", "dbo.users", dbdriver.ImportRequest{
			Mode: mode, Columns: []string{"id", "name"}, Keys: []string{"id"}, Rows: [][]dbdriver.ImportValue{texts("1", "A")},
		})
		require.NoError(t, err)
		for _, statement := range statements {
			assert.NotRegexp(t, `(?i)\bDECLARE\b|\b(SET|SELECT)\s+@`, statement)
		}
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// 多行 INSERT 的 VALUES 在 SQL Server 里最多 1000 行（多了报 Msg 10738）：行再短、上限再大，每条也
// 不超过 1000 行。
func TestImportExtendedInsertStaysWithinTheRowLimitForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectImportColumns(mock, "dbo", "users", importColumn{"id", "int", false})
	rows := make([][]dbdriver.ImportValue, 2001)
	for i := range rows {
		rows[i] = texts("1")
	}

	statements, err := session.ImportStatements(context.Background(), "appdb", "dbo.users", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"id"}, Rows: rows, ExtendedInsert: true, MaxStatementSizeKB: 1024,
	})
	require.NoError(t, err)
	require.Len(t, statements, 3)
	for i, want := range []int{1000, 1000, 1} {
		assert.Equal(t, want, strings.Count(statements[i], "('1')"), "statement %d", i)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// 打开表时按 SQL Server 的类型系统给出列的类别：image 是二进制，smalldatetime 是日期时间。
func TestOpenTableReportsColumnCategoriesForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(primaryKeysSQL).WithArgs("dbo", "files").WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}))
	expectImportColumns(mock, "dbo", "files",
		importColumn{"id", "int", true}, importColumn{"price", "money", false}, importColumn{"born", "date", false},
		importColumn{"seen", "smalldatetime", false}, importColumn{"at", "datetime2", false},
		importColumn{"scan", "image", false}, importColumn{"hash", "varbinary", false}, importColumn{"name", "nvarchar", false})
	mock.ExpectQuery("SELECT COUNT(*) FROM [dbo].[files]").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	mock.ExpectQuery("SELECT TOP 10 * FROM [dbo].[files]").
		WillReturnRows(sqlmock.NewRows([]string{"id", "price", "born", "seen", "at", "scan", "hash", "name"}))

	opened, err := session.OpenTable(context.Background(), "appdb", "dbo.files", 10)
	require.NoError(t, err)
	assert.Equal(t, map[string]dbdriver.ColumnCategory{
		"id": dbdriver.CategoryNumber, "price": dbdriver.CategoryNumber, "born": dbdriver.CategoryDate,
		"seen": dbdriver.CategoryDateTime, "at": dbdriver.CategoryDateTime,
		"scan": dbdriver.CategoryBinary, "hash": dbdriver.CategoryBinary, "name": dbdriver.CategoryText,
	}, opened.ColumnCategories)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 请求里的列不在表中（表已不存在时一列都没有）：生成失败，不给出任何语句。
func TestImportRefusesColumnsNotInTheTableForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectImportColumns(mock, "dbo", "users", plainUsers...)

	_, err := session.ImportStatements(context.Background(), "appdb", "dbo.users", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"ghost"}, Rows: [][]dbdriver.ImportValue{texts("1")},
	})
	require.ErrorContains(t, err, "ghost")
	require.NoError(t, mock.ExpectationsWereMet())
}
