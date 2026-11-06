package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

const sqlModeSQL = "SELECT @@SESSION.sql_mode"

func expectColumns(mock sqlmock.Sqlmock, tableRef string, columns ...[2]string) {
	rows := sqlmock.NewRows([]string{"Field", "Type", "Null", "Key", "Default", "Extra"})
	for _, c := range columns {
		rows.AddRow(c[0], c[1], "YES", "", nil, "")
	}
	mock.ExpectQuery("SHOW FULL COLUMNS FROM " + tableRef).WillReturnRows(rows)
}

// 会话的 sql_mode 不含 NO_BACKSLASH_ESCAPES 时反斜杠是转义符：成对写，读回的才是用户给的
// 'C:\new\table\x'（否则 \n、\t 成了换行与制表符）。表以 `库`.`表` 引用，数字照旧带引号。
func TestRowStatementsEscapeBackslashesUnderTheDefaultSQLMode(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`appdb`.`users`", [2]string{"id", "int"}, [2]string{"path", "varchar(255)"})
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).
		AddRow("ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,ERROR_FOR_DIVISION_BY_ZERO"))

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "users", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "path"},
		Rows:    []map[string]any{{"id": float64(1), "path": `C:\new\table\x`}, {"id": float64(2), "path": "O'Reilly"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"INSERT INTO `appdb`.`users` (`id`, `path`) VALUES ('1', 'C:\\\\new\\\\table\\\\x');",
		"INSERT INTO `appdb`.`users` (`id`, `path`) VALUES ('2', 'O''Reilly');",
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// NO_BACKSLASH_ESCAPES 下反斜杠是普通字符：原样写。UPDATE 只改一行用 LIMIT 1。
func TestRowStatementsKeepBackslashesUnderNoBackslashEscapes(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`appdb`.`users`", [2]string{"id", "int"}, [2]string{"path", "text"})
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).
		AddRow("STRICT_TRANS_TABLES,NO_BACKSLASH_ESCAPES"))

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "users", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"id", "path"},
		Rows:        []map[string]any{{"id": float64(2), "path": `C:\new`}},
		PrimaryKeys: []string{"id"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE `appdb`.`users` SET `id` = '2', `path` = 'C:\\new' WHERE `id` = '2' LIMIT 1;"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 与改动前前端生成的文本逐字一致（原 frontend/src/__tests__/tableExport.test.ts 的断言迁到这里）：
// 数字带引号、NULL 与缺列都是 NULL、空串仍是空的字符串字面量、单引号成对、中文原样。
func TestRowStatementsMatchTheFormerFrontendTextForMySQL(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	columns := [][2]string{{"id", "int"}, {"name", "varchar(64)"}, {"missing", "varchar(64)"}}
	mode := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).AddRow("STRICT_TRANS_TABLES")
	}

	expectColumns(mock, "`appdb`.`users`", columns...)
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(mode())
	statements, err := dbdrivertest.RowStatements(ctx, session, "appdb", "users", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "name", "missing"},
		Rows: []map[string]any{
			{"id": float64(1), "name": "Alice", "missing": nil},
			{"id": float64(2), "name": "O'Reilly", "missing": ""},
			{"id": float64(3), "name": "中文"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"INSERT INTO `appdb`.`users` (`id`, `name`, `missing`) VALUES ('1', 'Alice', NULL);",
		"INSERT INTO `appdb`.`users` (`id`, `name`, `missing`) VALUES ('2', 'O''Reilly', '');",
		"INSERT INTO `appdb`.`users` (`id`, `name`, `missing`) VALUES ('3', '中文', NULL);",
	}, statements)

	expectColumns(mock, "`appdb`.`users`", columns...)
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(mode())
	statements, err = dbdrivertest.RowStatements(ctx, session, "appdb", "users", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"id", "name"},
		Rows:        []map[string]any{{"id": float64(2), "name": "O'Reilly", "missing": ""}},
		PrimaryKeys: []string{"id"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE `appdb`.`users` SET `id` = '2', `name` = 'O''Reilly' WHERE `id` = '2' LIMIT 1;"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 名字里带点的表是一个名字：`appdb`.`a.b`，而不是 `appdb`.`a`.`b`。
func TestRowStatementsQuoteADottedTableAsOneName(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`appdb`.`a.b`", [2]string{"id", "int"})
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).AddRow(""))

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "a.b", dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": float64(1)}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO `appdb`.`a.b` (`id`) VALUES ('1');"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 二进制列（binary / varbinary / blob 系列）里表格带回的值是文本，与改动前一样写成字符串字面量
// （反斜杠照样按 sql_mode 转义），不改写成 X'…'。
func TestRowStatementsWriteBinaryColumnTextAsStringsForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`appdb`.`files`", [2]string{"id", "int"}, [2]string{"payload", "varbinary(16)"}, [2]string{"body", "longblob"})
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).AddRow(""))

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "appdb", "files", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "payload", "body"},
		Rows:    []map[string]any{{"id": float64(1), "payload": "AB", "body": `C:\`}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO `appdb`.`files` (`id`, `payload`, `body`) VALUES ('1', 'AB', 'C:\\\\');"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}
