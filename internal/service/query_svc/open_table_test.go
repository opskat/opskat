package query_svc

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/glebarez/go-sqlite"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestOpenTable_MySQL(t *testing.T) {
	Convey("OpenTable MySQL 一次返回 4 部分数据", t, func() {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(false))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		mock.MatchExpectationsInOrder(true)

		// 1. primary keys
		mock.ExpectQuery("SHOW KEYS FROM `mydb`.`users` WHERE Key_name = 'PRIMARY'").
			WillReturnRows(sqlmock.NewRows([]string{"Table", "Column_name"}).
				AddRow("users", "id"))

		// 2. columns
		mock.ExpectQuery("SHOW COLUMNS FROM `mydb`.`users`").
			WillReturnRows(sqlmock.NewRows([]string{"Field", "Type", "Null", "Key", "Default", "Extra"}).
				AddRow("id", "int", "NO", "PRI", nil, "auto_increment").
				AddRow("name", "varchar(255)", "YES", "", "anon", ""))

		// 3. count
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `mydb`.`users`").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(42))

		// 4. first page
		mock.ExpectQuery("SELECT \\* FROM `mydb`.`users` LIMIT 10 OFFSET 0").
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "alice").
				AddRow(2, "bob"))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverMySQL, "mydb", "users", 10)
		So(err, ShouldBeNil)
		So(res, ShouldNotBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
		So(res.Columns, ShouldResemble, []string{"id", "name"})
		So(res.ColumnTypes["id"], ShouldEqual, "int")
		So(res.ColumnTypes["name"], ShouldEqual, "varchar(255)")
		So(res.TotalCount, ShouldEqual, 42)
		So(res.PageSize, ShouldEqual, 10)
		So(len(res.FirstPage), ShouldEqual, 2)
		So(res.FirstPage[0]["name"], ShouldEqual, "alice")

		// rules
		var idRule, nameRule TableColumnRule
		for _, r := range res.ColumnRules {
			if r.Name == "id" {
				idRule = r
			}
			if r.Name == "name" {
				nameRule = r
			}
		}
		So(idRule.Nullable, ShouldBeFalse)
		So(idRule.AutoIncrement, ShouldBeTrue)
		So(nameRule.Nullable, ShouldBeTrue)
		So(nameRule.HasDefault, ShouldBeTrue) // "anon"
		So(nameRule.AutoIncrement, ShouldBeFalse)

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOpenTable_PostgreSQL(t *testing.T) {
	Convey("OpenTable PG 4 条 SQL 顺序正确", t, func() {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(false))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		mock.MatchExpectationsInOrder(true)

		mock.ExpectQuery("information_schema.table_constraints").
			WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))

		mock.ExpectQuery("information_schema.columns").
			WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default"}).
				AddRow("id", "integer", "int4", "NO", "nextval('seq')").
				AddRow("name", "text", "text", "YES", nil))

		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "users"`).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))

		mock.ExpectQuery(`SELECT \* FROM "users" LIMIT 20 OFFSET 0`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice"))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverPostgreSQL, "mydb", "users", 20)
		So(err, ShouldBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
		So(res.TotalCount, ShouldEqual, 5)
		So(res.ColumnTypes["name"], ShouldEqual, "text")
		So(len(res.FirstPage), ShouldEqual, 1)

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOpenTable_PostgreSQL_SchemaQualified(t *testing.T) {
	Convey("OpenTable PG 接收 schema.table 时按 schema 拆分查 information_schema", t, func() {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(false))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		mock.MatchExpectationsInOrder(true)

		// information_schema 查询必须按 schema 拆分:table_schema='reporting' 且 table_name='events',
		// 而不是把 'reporting.events' 整体塞进 table_name。
		mock.ExpectQuery(`tc\.table_schema = \$1.*tc\.table_name = \$2`).
			WithArgs("reporting", "events").
			WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))
		mock.ExpectQuery(`table_schema = \$1.*table_name = \$2`).
			WithArgs("reporting", "events").
			WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default"}).
				AddRow("id", "integer", "int4", "NO", nil))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "reporting"."events"`).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(3))
		mock.ExpectQuery(`SELECT \* FROM "reporting"."events" LIMIT 10 OFFSET 0`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverPostgreSQL, "mydb", "reporting.events", 10)
		So(err, ShouldBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
		So(res.TotalCount, ShouldEqual, 3)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestOpenTableMSSQL(t *testing.T) {
	Convey("MSSQL OpenTable 按 schema.table 查 information_schema 并用 [schema].[table] 取数", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		// 表标识来自侧边栏的 schema.table（如 "dbo.users"），按 schema + table
		// 两段过滤 INFORMATION_SCHEMA，避免同名表跨 schema 时列/主键混淆。
		pkSQL := "SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
			"JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
			"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA " +
			"WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
			"AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2 " +
			"ORDER BY kcu.ORDINAL_POSITION"
		mock.ExpectQuery(pkSQL).
			WithArgs("dbo", "users").
			WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))

		colSQL := "SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT " +
			"FROM INFORMATION_SCHEMA.COLUMNS " +
			"WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 " +
			"ORDER BY ORDINAL_POSITION"
		mock.ExpectQuery(colSQL).
			WithArgs("dbo", "users").
			WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT"}).
				AddRow("id", "int", "NO", nil).
				AddRow("name", "varchar", "YES", nil))

		mock.ExpectQuery("SELECT COUNT(*) FROM [dbo].[users]").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

		mock.ExpectQuery("SELECT TOP 10 * FROM [dbo].[users]").
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "alice").AddRow(2, "bob"))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverMSSQL, "appdb", "dbo.users", 10)
		So(err, ShouldBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
		So(res.Columns, ShouldContain, "id")
		So(res.TotalCount, ShouldEqual, 2)
	})

	Convey("MSSQL OpenTable 裸表名默认 dbo schema", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		pkSQL := "SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
			"JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
			"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA " +
			"WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
			"AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2 " +
			"ORDER BY kcu.ORDINAL_POSITION"
		mock.ExpectQuery(pkSQL).
			WithArgs("dbo", "users").
			WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))

		colSQL := "SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT " +
			"FROM INFORMATION_SCHEMA.COLUMNS " +
			"WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 " +
			"ORDER BY ORDINAL_POSITION"
		mock.ExpectQuery(colSQL).
			WithArgs("dbo", "users").
			WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT"}).
				AddRow("id", "int", "NO", nil))

		mock.ExpectQuery("SELECT COUNT(*) FROM [users]").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))

		mock.ExpectQuery("SELECT TOP 10 * FROM [users]").
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverMSSQL, "appdb", "users", 10)
		So(err, ShouldBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
	})
}

func TestOpenTableSQLite(t *testing.T) {
	Convey("SQLite OpenTable 用 pragma_table_info", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		pkSQL := "SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk"
		mock.ExpectQuery(pkSQL).
			WithArgs("users").
			WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("id"))

		colSQL := `SELECT name, type, CASE "notnull" WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM pragma_table_info(?)`
		mock.ExpectQuery(colSQL).
			WithArgs("users").
			WillReturnRows(sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value"}).
				AddRow("id", "INTEGER", "NO", nil).
				AddRow("name", "TEXT", "YES", nil))

		mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

		mock.ExpectQuery(`SELECT * FROM "users" LIMIT 10 OFFSET 0`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "alice").AddRow(2, "bob"))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverSQLite, "", "users", 10)
		So(err, ShouldBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
		So(res.Columns, ShouldContain, "id")
	})

	Convey("SQLite OpenTable 在真实 SQLite 连接上可查询列信息", t, func() {
		db, err := sql.Open("sqlite", ":memory:")
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)

		_, err = db.ExecContext(
			context.Background(),
			`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL DEFAULT 'anon')`,
		)
		So(err, ShouldBeNil)
		_, err = db.ExecContext(context.Background(), `INSERT INTO users (name) VALUES ('alice')`)
		So(err, ShouldBeNil)

		res, err := OpenTable(context.Background(), db, asset_entity.DriverSQLite, "main", "users", 10)
		So(err, ShouldBeNil)
		So(res.PrimaryKeys, ShouldResemble, []string{"id"})
		So(res.Columns, ShouldResemble, []string{"id", "name"})
		So(res.ColumnTypes["name"], ShouldEqual, "TEXT")
		So(res.TotalCount, ShouldEqual, 1)
		So(res.FirstPage, ShouldHaveLength, 1)
	})
}

func TestOpenTable_PageSizeDefault(t *testing.T) {
	Convey("pageSize<=0 时回退到 1000", t, func() {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(false))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		mock.MatchExpectationsInOrder(true)

		mock.ExpectQuery("SHOW KEYS").WillReturnRows(sqlmock.NewRows([]string{"Column_name"}))
		mock.ExpectQuery("SHOW COLUMNS").WillReturnRows(sqlmock.NewRows([]string{"Field", "Type", "Null", "Default", "Extra"}))
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("LIMIT 1000 OFFSET 0").WillReturnRows(sqlmock.NewRows([]string{"id"}))

		res, err := OpenTable(context.Background(), db, asset_entity.DriverMySQL, "mydb", "users", 0)
		So(err, ShouldBeNil)
		So(res.PageSize, ShouldEqual, 1000)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
