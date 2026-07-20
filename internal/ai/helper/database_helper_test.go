package helper

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/glebarez/go-sqlite"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestFormatRowsJSONContracts(t *testing.T) {
	tests := []struct {
		name       string
		paged      bool
		totalCount int
	}{
		{name: "unpaged", paged: false},
		{name: "paged", paged: true, totalCount: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()

			mock.ExpectQuery("SELECT").WillReturnRows(
				sqlmock.NewRows([]string{"name", "nullable"}).
					AddRow([]byte("alice"), nil),
			)
			rows, err := db.Query("SELECT")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()

			var result string
			if tt.paged {
				result, err = formatRowsPagedJSON(rows, tt.totalCount)
			} else {
				result, err = formatRowsJSON(rows)
			}
			if err != nil {
				t.Fatal(err)
			}

			var got map[string]any
			if err := json.Unmarshal([]byte(result), &got); err != nil {
				t.Fatal(err)
			}
			if got["count"] != float64(1) {
				t.Fatalf("count = %#v, want 1", got["count"])
			}
			if tt.paged {
				if got["total_count"] != float64(tt.totalCount) {
					t.Fatalf("total_count = %#v, want %d", got["total_count"], tt.totalCount)
				}
			} else if _, ok := got["total_count"]; ok {
				t.Fatal("unpaged result unexpectedly contains total_count")
			}
			resultRows := got["rows"].([]any)
			row := resultRows[0].(map[string]any)
			if row["name"] != "alice" || row["nullable"] != nil {
				t.Fatalf("row = %#v, want converted bytes and preserved nil", row)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type recordCloser struct {
	name   string
	order  *[]string
	closed bool
}

func (c *recordCloser) Close() error {
	c.closed = true
	*c.order = append(*c.order, c.name)
	return nil
}

func TestCloseDatabaseConnOrder(t *testing.T) {
	Convey("closeDatabaseConn 先关 *sql.DB 再关传输层 closer", t, func() {
		// WAL 远端库的 db.Close() 会回调 VFS 删除 -wal/-shm，所以 db 必须先于
		// 注销该 VFS 的 closer 关闭，否则回调会命中已注销的 VFS 而 panic。
		var order []string
		db := &recordCloser{name: "db", order: &order}
		closer := &recordCloser{name: "closer", order: &order}

		closeDatabaseConn(db, closer)

		So(order, ShouldResemble, []string{"db", "closer"})
		So(db.closed, ShouldBeTrue)
		So(closer.closed, ShouldBeTrue)
	})

	Convey("closeDatabaseConn 容忍 nil db / nil closer", t, func() {
		So(func() { closeDatabaseConn(nil, nil) }, ShouldNotPanic)

		var order []string
		closer := &recordCloser{name: "closer", order: &order}
		closeDatabaseConn(nil, closer)
		So(order, ShouldResemble, []string{"closer"})
	})
}

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
