package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb/sqldbtest"
)

// 打开表一次返回 4 部分数据：主键、列（类型与约束）、总行数、第一页，按这个顺序各一条语句。
func TestOpenTable(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SHOW KEYS FROM `mydb`.`users` WHERE Key_name = 'PRIMARY'").
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Column_name"}).AddRow("users", "id"))
	mock.ExpectQuery("SHOW FULL COLUMNS FROM `mydb`.`users`").
		WillReturnRows(sqlmock.NewRows([]string{"Field", "Type", "Null", "Key", "Default", "Extra"}).
			AddRow("id", "int", "NO", "PRI", nil, "auto_increment").
			AddRow("name", "varchar(255)", "YES", "", "anon", ""))
	mock.ExpectQuery("SELECT COUNT(*) FROM `mydb`.`users`").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(42))
	mock.ExpectQuery("SELECT * FROM `mydb`.`users` LIMIT 10 OFFSET 0").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice").AddRow(2, "bob"))

	opened, err := session.OpenTable(context.Background(), "mydb", "users", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, []string{"id", "name"}, opened.FirstPage.Columns)
	assert.Equal(t, map[string]string{"id": "int", "name": "varchar(255)"}, opened.ColumnTypes)
	assert.Equal(t, int64(42), opened.TotalCount)
	assert.Equal(t, [][]any{{int64(1), "alice"}, {int64(2), "bob"}}, opened.FirstPage.Rows)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "id", AutoIncrement: true},
		{Name: "name", Nullable: true, HasDefault: true}, // 默认值 "anon"
	}, opened.ColumnRules)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 翻页：表以 `库`.`表` 引用，列名加反引号（内部的反引号成对转义），分页用 LIMIT / OFFSET。
func TestTableRows(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	row := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}).AddRow(1) }

	mock.ExpectQuery("SELECT * FROM `mydb`.`users` LIMIT 50 OFFSET 100").WillReturnRows(row())
	_, err := session.TableRows(ctx, "mydb", "users", dbdriver.TableQuery{}, dbdriver.Page{Index: 2, Size: 50})
	require.NoError(t, err)

	mock.ExpectQuery("SELECT * FROM `my``db`.`us``ers` WHERE `na``me` = 'O''Reilly' ORDER BY `id` DESC, `na``me` ASC LIMIT 50 OFFSET 0").
		WillReturnRows(row())
	rows, err := session.TableRows(ctx, "my`db", "us`ers", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{
			{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "na`me", Operator: dbdriver.OpEqual, Value: "O'Reilly"},
		},
		Sorts: []dbdriver.SortItem{
			{Column: "id", Direction: dbdriver.SortDescending},
			{Column: "na`me", Direction: dbdriver.SortAscending},
		},
	}, dbdriver.Page{Index: 0, Size: 50})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(1)}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilterSemantics(t *testing.T) {
	sqldbtest.RunFilterSemantics(t, sqldbtest.Dialect{
		NewSession: newSession,
		Quote:      func(name string) string { return "`" + name + "`" },
		Database:   "mydb",
		Table:      "users",
		TableRef:   "`mydb`.`users`",
	})
}
