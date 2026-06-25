package query_svc

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	. "github.com/smartystreets/goconvey/convey"
)

func TestExportRowsToXlsx_RoundTrip(t *testing.T) {
	Convey("ExportRowsToXlsx 导出的工作簿可被 ParseXlsx 原样读回", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)

		ctx := context.Background()
		_, err = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, note TEXT)`)
		So(err, ShouldBeNil)
		_, err = db.ExecContext(ctx, `INSERT INTO t (id, name, note) VALUES (1, 'alice', 'hi'), (2, 'bob', NULL)`)
		So(err, ShouldBeNil)

		conn, err := db.Conn(ctx)
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		var buf bytes.Buffer
		err = ExportRowsToXlsx(ctx, conn, "SELECT id, name, note FROM t ORDER BY id", &buf)
		So(err, ShouldBeNil)
		So(buf.Len(), ShouldBeGreaterThan, 0)

		parsed, err := ParseXlsx(bytes.NewReader(buf.Bytes()), "")
		So(err, ShouldBeNil)
		So(parsed.Headers, ShouldResemble, []string{"id", "name", "note"})
		So(parsed.Rows, ShouldHaveLength, 2)
		// Numbers come back as their formatted string; a NULL cell pads to "".
		So(parsed.Rows[0], ShouldResemble, []string{"1", "alice", "hi"})
		So(parsed.Rows[1], ShouldResemble, []string{"2", "bob", ""})
	})
}

func TestParseXlsx_EmptySheet(t *testing.T) {
	Convey("ParseXlsx 处理空结果集时返回空表而非报错", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)

		ctx := context.Background()
		_, err = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT)`)
		So(err, ShouldBeNil)

		conn, err := db.Conn(ctx)
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		var buf bytes.Buffer
		err = ExportRowsToXlsx(ctx, conn, "SELECT id, name FROM t", &buf)
		So(err, ShouldBeNil)

		parsed, err := ParseXlsx(bytes.NewReader(buf.Bytes()), "")
		So(err, ShouldBeNil)
		So(parsed.Headers, ShouldResemble, []string{"id", "name"})
		So(parsed.Rows, ShouldHaveLength, 0)
	})
}
