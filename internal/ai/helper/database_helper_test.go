package helper

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
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
