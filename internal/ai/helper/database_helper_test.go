package helper

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/glebarez/go-sqlite"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestExecuteSQLSQLitePragmaQuery(t *testing.T) {
	Convey("ExecuteSQL treats SQLite PRAGMA as a row-returning query", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		// :memory: 是 per-connection 的,必须固定单连接,否则 CREATE 和
		// 后续 PRAGMA 会落到不同的内存库上,造成测试偶发失败。
		db.SetMaxOpenConns(1)

		_, err = db.ExecContext(context.Background(), `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		So(err, ShouldBeNil)

		result, err := ExecuteSQL(context.Background(), db, `PRAGMA table_info("users")`)
		So(err, ShouldBeNil)
		So(result, ShouldContainSubstring, `"columns"`)
		So(result, ShouldContainSubstring, `"rows"`)
		So(result, ShouldContainSubstring, `"name"`)
		So(strings.Contains(result, "affected_rows"), ShouldBeFalse)
	})
}

func TestExecuteSQLPagedDialect(t *testing.T) {
	Convey("MSSQL 分页用 OFFSET/FETCH 而非 LIMIT", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		mock.ExpectQuery(
			"SELECT * FROM (SELECT * FROM users) AS _t ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))

		_, err = ExecuteSQLPaged(context.Background(), db, "SELECT * FROM users", 0, 10, asset_entity.DriverMSSQL)
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("MSSQL 分页保留顶层 ORDER BY 且 count 去掉 ORDER BY", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		mock.ExpectQuery("SELECT * FROM users ORDER BY id DESC OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))

		_, err = ExecuteSQLPaged(context.Background(), db, "SELECT * FROM users ORDER BY id DESC", 2, 10, asset_entity.DriverMSSQL)
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("MySQL/其他 driver 仍用 LIMIT/OFFSET（不受影响）", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		mock.ExpectQuery("SELECT * FROM (SELECT * FROM users) AS _t LIMIT 10 OFFSET 20").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

		_, err = ExecuteSQLPaged(context.Background(), db, "SELECT * FROM users", 2, 10, asset_entity.DriverMySQL)
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}
