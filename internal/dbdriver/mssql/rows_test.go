package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func expectColumns(mock sqlmock.Sqlmock, schema, table string, columns ...[2]string) {
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT"})
	for _, c := range columns {
		rows.AddRow(c[0], c[1], "YES", nil)
	}
	mock.ExpectQuery(columnsSQL).WithArgs(schema, table).WillReturnRows(rows)
}

// 复制 / 导出为 INSERT：表以 [schema].[table] 引用；nchar / nvarchar / ntext 列的文本带 N 前缀
// （没有它，库排序规则不是 Unicode 时中文写进去就成了 ??），其他文本列不加；二进制写成 0x…；
// 数字与 NULL 照旧。
func TestRowStatementsInsertWritesUnicodeTextAndBinaryForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "dbo", "users",
		[2]string{"id", "int"}, [2]string{"nv", "nvarchar"}, [2]string{"nc", "NCHAR"}, [2]string{"nt", "ntext"},
		[2]string{"v", "varchar"}, [2]string{"bin", "varbinary"}, [2]string{"img", "image"})

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "dbo.users", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "nv", "nc", "nt", "v", "bin", "img"},
		Rows: []map[string]any{
			{"id": float64(1), "nv": "中文Ünï", "nc": "O'Reilly", "nt": `C:\new`, "v": "abc", "bin": "\x01\xff", "img": nil},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"INSERT INTO [dbo].[users] ([id], [nv], [nc], [nt], [v], [bin], [img]) VALUES " +
			`('1', N'中文Ünï', N'O''Reilly', N'C:\new', 'abc', 0x01ff, NULL);`,
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 复制为 UPDATE：只改一行用 UPDATE TOP (1)；按 nvarchar 列定位时同样带 N 前缀，否则 WHERE 会
// 匹配到被写坏的行。
func TestRowStatementsUpdateLocatesUnicodeKeysForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "dbo", "users", [2]string{"code", "nvarchar"}, [2]string{"qty", "int"})

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "dbo.users", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"code", "qty"},
		Rows:        []map[string]any{{"code": "中文", "qty": float64(3)}},
		PrimaryKeys: []string{"code"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE TOP (1) [dbo].[users] SET [code] = N'中文', [qty] = '3' WHERE [code] = N'中文';"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 非 Unicode 列与改动前前端生成的文本逐字一致（原 frontend/src/__tests__/tableExport.test.ts 的断言）。
func TestRowStatementsMatchTheFormerFrontendTextForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "dbo", "users", [2]string{"id", "int"}, [2]string{"name", "varchar"})

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "dbo.users", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"id", "name"},
		Rows:        []map[string]any{{"id": float64(2), "name": "O'Reilly"}},
		PrimaryKeys: []string{"id"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE TOP (1) [dbo].[users] SET [id] = '2', [name] = 'O''Reilly' WHERE [id] = '2';"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 名字里带点的表：只在第一个点处拆开 schema，余下的整体是表名——列查询、浏览与生成的语句都一样。
func TestDottedTableNameIsOneNameForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()

	expectColumns(mock, "dbo", "a.b", [2]string{"id", "int"})
	statements, err := dbdrivertest.RowStatements(ctx, session, "appdb", "dbo.a.b", dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": float64(1)}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO [dbo].[a.b] ([id]) VALUES ('1');"}, statements)

	mock.ExpectQuery("SELECT * FROM [dbo].[a.b] ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	_, err = session.TableRows(ctx, "appdb", "dbo.a.b", dbdriver.TableQuery{}, dbdriver.Page{Size: 10})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
