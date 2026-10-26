package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 表格编辑：新增行只写填写过的列，一列都没填写成 () VALUES ()；改动与删除有没有主键都带 LIMIT 1，
// 没有主键时按原行的全部列定位（NULL 写成 IS NULL）。与改动前前端生成的文本逐字一致（原
// tableEdit.test.ts / tableSql.test.ts 的断言），反斜杠按会话的 sql_mode 转义。
func TestGridEditStatementsForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`appdb`.`users`",
		[2]string{"id", "int"}, [2]string{"name", "varchar(64)"}, [2]string{"email", "varchar(64)"}, [2]string{"deleted_at", "datetime"})
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).AddRow("STRICT_TRANS_TABLES"))

	statements, err := session.GridEditStatements(context.Background(), "appdb", "users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name", "email", "deleted_at"},
		Inserts: []dbdriver.GridInsert{
			{Values: []dbdriver.ColumnValue{{Column: "name", Value: "Alice"}, {Column: "email", Value: "alice@example.com"}}},
			{},
		},
		Updates: []dbdriver.GridUpdate{{
			Row: map[string]any{"id": float64(7), "name": "O'Reilly", "email": nil, "deleted_at": nil},
			Set: []dbdriver.ColumnValue{{Column: "email", Value: `C:\new`}, {Column: "name", Value: "x"}},
		}},
		Deletes: []map[string]any{{"id": float64(7), "name": "O'Reilly", "email": "e", "deleted_at": nil}},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.GridEditStatements{
		Inserts: []string{
			"INSERT INTO `appdb`.`users` (`name`, `email`) VALUES ('Alice', 'alice@example.com');",
			"INSERT INTO `appdb`.`users` () VALUES ();",
		},
		Updates: []string{
			"UPDATE `appdb`.`users` SET `email` = 'C:\\\\new', `name` = 'x' WHERE `id` = '7' AND `name` = 'O''Reilly' AND `email` IS NULL AND `deleted_at` IS NULL LIMIT 1;",
		},
		Deletes: []string{
			"DELETE FROM `appdb`.`users` WHERE `id` = '7' AND `name` = 'O''Reilly' AND `email` = 'e' AND `deleted_at` IS NULL LIMIT 1;",
		},
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGridEditLocatesByPrimaryKeyForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`db`.`t`", [2]string{"id", "int"}, [2]string{"n", "text"})
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).AddRow(""))

	statements, err := session.GridEditStatements(context.Background(), "db", "t", dbdriver.GridEditRequest{
		Columns: []string{"id", "n"}, PrimaryKeys: []string{"id"},
		Updates: []dbdriver.GridUpdate{{Row: map[string]any{"id": float64(7), "n": "old"}, Set: []dbdriver.ColumnValue{{Column: "n", Value: "x"}}}},
		Deletes: []map[string]any{{"id": float64(7), "n": "old"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE `db`.`t` SET `n` = 'x' WHERE `id` = '7' LIMIT 1;"}, statements.Updates)
	assert.Equal(t, []string{"DELETE FROM `db`.`t` WHERE `id` = '7' LIMIT 1;"}, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}
