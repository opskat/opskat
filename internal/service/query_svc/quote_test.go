package query_svc

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestQuoteIdent(t *testing.T) {
	Convey("QuoteIdent", t, func() {
		Convey("MySQL 用反引号", func() {
			So(QuoteIdent("user", asset_entity.DriverMySQL), ShouldEqual, "`user`")
		})
		Convey("PostgreSQL 用双引号", func() {
			So(QuoteIdent("user", asset_entity.DriverPostgreSQL), ShouldEqual, `"user"`)
		})
		Convey("MySQL 反引号转义", func() {
			So(QuoteIdent("a`b", asset_entity.DriverMySQL), ShouldEqual, "`a``b`")
		})
		Convey("PostgreSQL 双引号转义", func() {
			So(QuoteIdent(`a"b`, asset_entity.DriverPostgreSQL), ShouldEqual, `"a""b"`)
		})
	})
}

func TestQuoteTableRef(t *testing.T) {
	Convey("QuoteTableRef", t, func() {
		Convey("MySQL 加 db 前缀", func() {
			So(QuoteTableRef("mydb", "users", asset_entity.DriverMySQL), ShouldEqual, "`mydb`.`users`")
		})
		Convey("PostgreSQL 只引用表名", func() {
			So(QuoteTableRef("mydb", "users", asset_entity.DriverPostgreSQL), ShouldEqual, `"users"`)
		})
		Convey("PostgreSQL 支持 schema.table", func() {
			So(QuoteTableRef("mydb", "public.users", asset_entity.DriverPostgreSQL), ShouldEqual, `"public"."users"`)
		})
	})
}

func TestSQLQuote(t *testing.T) {
	Convey("SQLQuote 转义单引号", t, func() {
		So(SQLQuote("hello"), ShouldEqual, `'hello'`)
		So(SQLQuote("it's"), ShouldEqual, `'it''s'`)
	})
}

func TestQuoteIdentMSSQL(t *testing.T) {
	Convey("MSSQL 用 [bracket]", t, func() {
		So(QuoteIdent("user", asset_entity.DriverMSSQL), ShouldEqual, "[user]")
		So(QuoteIdent("a]b", asset_entity.DriverMSSQL), ShouldEqual, "[a]]b]")
	})
}

func TestQuoteIdentSQLite(t *testing.T) {
	Convey("SQLite 用 \"double\"", t, func() {
		So(QuoteIdent("user", asset_entity.DriverSQLite), ShouldEqual, `"user"`)
		So(QuoteIdent(`a"b`, asset_entity.DriverSQLite), ShouldEqual, `"a""b"`)
	})
}

func TestQuoteTableRefMSSQL(t *testing.T) {
	// MSSQL 两段式 [db].[table] 会被解释为 schema.object（schema=db），导致
	// "Invalid object name"。连接已经通过 DSN database= 限定了 catalog，所以
	// 这里与 PostgreSQL 一致：忽略 database，只按 schema.table 加方括号。
	Convey("MSSQL 裸表名只引用表名（不把 db 当 schema）", t, func() {
		So(QuoteTableRef("appdb", "users", asset_entity.DriverMSSQL), ShouldEqual, "[users]")
	})
	Convey("MSSQL 支持 schema.table（输出 [schema].[table]）", t, func() {
		So(QuoteTableRef("appdb", "dbo.users", asset_entity.DriverMSSQL), ShouldEqual, "[dbo].[users]")
	})
}

func TestQuoteTableRefSQLite(t *testing.T) {
	Convey("SQLite 有 database 时按 schema.table 引用", t, func() {
		So(QuoteTableRef("main", "users", asset_entity.DriverSQLite), ShouldEqual, `"main"."users"`)
	})
	Convey("SQLite 无 database 时只引用表名", t, func() {
		So(QuoteTableRef("", "users", asset_entity.DriverSQLite), ShouldEqual, `"users"`)
	})
}
