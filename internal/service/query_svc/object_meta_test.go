package query_svc

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestGetTableMetadata_SQLite(t *testing.T) {
	Convey("GetTableMetadata 返回 SQLite 表的列/索引/外键", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)

		ctx := context.Background()
		_, err = db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
		So(err, ShouldBeNil)
		_, err = db.ExecContext(ctx, `CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id),
			code TEXT
		)`)
		So(err, ShouldBeNil)
		_, err = db.ExecContext(ctx, `CREATE UNIQUE INDEX idx_orders_code ON orders(code)`)
		So(err, ShouldBeNil)

		conn, err := db.Conn(ctx)
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		meta, err := GetTableMetadata(ctx, conn, asset_entity.DriverSQLite, "main", "orders")
		So(err, ShouldBeNil)

		So(len(meta.Columns), ShouldEqual, 3)
		So(meta.Columns[0].Name, ShouldEqual, "id")
		So(meta.Columns[0].PrimaryKey, ShouldBeTrue)
		So(meta.Columns[1].Name, ShouldEqual, "user_id")
		So(meta.Columns[1].Nullable, ShouldBeFalse)

		var hasUnique bool
		for _, idx := range meta.Indexes {
			if idx.Unique && len(idx.Columns) == 1 && idx.Columns[0] == "code" {
				hasUnique = true
			}
		}
		So(hasUnique, ShouldBeTrue)

		So(len(meta.ForeignKeys), ShouldEqual, 1)
		So(meta.ForeignKeys[0].Column, ShouldEqual, "user_id")
		So(meta.ForeignKeys[0].ReferencedTable, ShouldEqual, "users")
		So(meta.ForeignKeys[0].ReferencedColumn, ShouldEqual, "id")
	})
}

func TestGetTableMetadata_SQLite_NoForeignKeys(t *testing.T) {
	Convey("无外键的表，foreignKeys 序列化为 [] 而非 null", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)

		ctx := context.Background()
		_, err = db.ExecContext(ctx, `CREATE TABLE plain (id INTEGER PRIMARY KEY, name TEXT)`)
		So(err, ShouldBeNil)

		conn, err := db.Conn(ctx)
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		meta, err := GetTableMetadata(ctx, conn, asset_entity.DriverSQLite, "main", "plain")
		So(err, ShouldBeNil)

		// A nil slice marshals to JSON null, which crashes the frontend's
		// meta.foreignKeys.length. The contract is an array, always.
		So(meta.ForeignKeys, ShouldNotBeNil)
		So(len(meta.ForeignKeys), ShouldEqual, 0)
		raw, err := json.Marshal(meta)
		So(err, ShouldBeNil)
		So(strings.Contains(string(raw), `"foreignKeys":[]`), ShouldBeTrue)
		So(strings.Contains(string(raw), `"foreignKeys":null`), ShouldBeFalse)
	})
}

func TestListDatabaseObjects_SQLite(t *testing.T) {
	Convey("ListDatabaseObjects 返回 SQLite 的视图与触发器", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)

		ctx := context.Background()
		_, err = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, n INTEGER)`)
		So(err, ShouldBeNil)
		_, err = db.ExecContext(ctx, `CREATE VIEW v_t AS SELECT id FROM t`)
		So(err, ShouldBeNil)
		_, err = db.ExecContext(ctx, `CREATE TRIGGER trg_t AFTER INSERT ON t BEGIN UPDATE t SET n = 1; END`)
		So(err, ShouldBeNil)

		conn, err := db.Conn(ctx)
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		objs, err := ListDatabaseObjects(ctx, conn, asset_entity.DriverSQLite, "main")
		So(err, ShouldBeNil)
		So(len(objs.Views), ShouldEqual, 1)
		So(objs.Views[0].Name, ShouldEqual, "v_t")
		So(len(objs.Triggers), ShouldEqual, 1)
		So(objs.Triggers[0].Name, ShouldEqual, "trg_t")
		So(len(objs.Procedures), ShouldEqual, 0)

		src, err := GetObjectSource(ctx, conn, asset_entity.DriverSQLite, "main", "view", "v_t", "")
		So(err, ShouldBeNil)
		So(src, ShouldContainSubstring, "CREATE VIEW")
	})
}
