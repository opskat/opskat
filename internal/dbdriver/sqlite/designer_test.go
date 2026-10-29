package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 表设计器的测试都在真实的库文件上：语句生成之后经 ExecuteBatch 执行，再读回文件确认结构。

func model(t *testing.T, session dbdriver.Session, table string) *dbdriver.AlterTableModel {
	t.Helper()
	m, err := session.AlterTableModel(context.Background(), "main", table)
	require.NoError(t, err)
	return m
}

func runAtomic(t *testing.T, session dbdriver.Session, table string, statements []string) {
	t.Helper()
	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: statements, Atomic: true, Database: "main", Table: table,
	})
	require.NoError(t, err)
	require.False(t, result.Failed(), "%+v", result.Executed)
}

// keepAll 把改表模型原样写成改表请求（每列、每个索引按现在的名字对上）。
func keepAll(m *dbdriver.AlterTableModel) dbdriver.AlterTableRequest {
	req := dbdriver.AlterTableRequest{Name: m.Name, Comment: m.Comment}
	for _, c := range m.Columns {
		req.Columns = append(req.Columns, dbdriver.AlterColumn{
			OriginalName: c.Name, Name: c.Name, Type: c.Type, Nullable: c.Nullable, DefaultValue: c.DefaultValue,
			Comment: c.Comment, DefaultKind: c.DefaultKind, AutoIncrement: c.AutoIncrement,
		})
	}
	for _, index := range m.Indexes {
		req.Indexes = append(req.Indexes, dbdriver.AlterIndex{OriginalName: index.Name, IndexDefinition: index})
	}
	return req
}

func column(name string) dbdriver.IndexColumn { return dbdriver.IndexColumn{Name: name} }

// 新建表：自增主键写在列上（INTEGER PRIMARY KEY 即 rowid），默认值按写法写（表达式不是常量、CURRENT_* 时加括号，
// SQLite 的 DEFAULT 只接受这样写），其余索引在建表之后按请求的次序
// 建（索引名带库名，表名不带——SQLite 的 CREATE INDEX 只认这种写法）；读回文件时自增、默认值写法与索引原样。
func TestCreateTableWithIndexesOnARealSQLiteDatabase(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE other (id INTEGER)`, `CREATE INDEX other_ix ON other (id)`)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "main", dbdriver.CreateTableRequest{
		Name: "users",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "INTEGER", AutoIncrement: true},
			{Name: "name", Type: "TEXT", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "it's"},
			{Name: "email", Type: "VARCHAR(255)", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
			{Name: "created_at", Type: "DATETIME", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "CURRENT_TIMESTAMP"},
			{Name: "score", Type: "REAL", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
			{Name: "code", Type: "TEXT", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "upper('a')"},
		},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "pk_users", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{column("id")}},
			{Name: "idx_users_email", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{column("email")}},
			{Name: "idx_users_created", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "created_at", Descending: true}, column("name")}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATE TABLE \"main\".\"users\" (\n" +
			"  \"id\" INTEGER PRIMARY KEY NOT NULL,\n" +
			"  \"name\" TEXT DEFAULT 'it''s' NOT NULL,\n" +
			"  \"email\" VARCHAR(255) DEFAULT NULL,\n" +
			"  \"created_at\" DATETIME DEFAULT CURRENT_TIMESTAMP,\n" +
			"  \"score\" REAL,\n" +
			"  \"code\" TEXT DEFAULT (upper('a'))\n" +
			")",
		`CREATE UNIQUE INDEX "main"."idx_users_email" ON "users" ("email")`,
		`CREATE INDEX "main"."idx_users_created" ON "users" ("created_at" DESC, "name")`,
	}, statements)
	runAtomic(t, session, "users", statements)

	assert.Equal(t, &dbdriver.AlterTableModel{
		Name: "users",
		Columns: []dbdriver.ColumnDefinition{
			{Name: "id", Type: "INTEGER", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true},
			{Name: "name", Type: "TEXT", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "it's"},
			{Name: "email", Type: "VARCHAR(255)", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
			{Name: "created_at", Type: "DATETIME", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "CURRENT_TIMESTAMP"},
			{Name: "score", Type: "REAL", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
			{Name: "code", Type: "TEXT", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "upper('a')"},
		},
		Indexes: []dbdriver.IndexDefinition{
			{Name: rowidPrimaryKeyName, Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{column("id")}, Undroppable: true},
			{Name: "idx_users_created", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "created_at", Descending: true}, column("name")}},
			{Name: "idx_users_email", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{column("email")}},
		},
		// 索引名与表、视图、触发器、其他表的索引同在库的名字空间里。
		TakenIndexNames: []string{"other", "other_ix", "users"},
	}, model(t, session, "users"))

	// 读出的样子原样交回不产生语句。
	result, err := session.AlterTableStatements(ctx, "main", "users", keepAll(model(t, session, "users")))
	require.NoError(t, err)
	assert.Empty(t, result.Statements)

	// 写进去的行拿到自增的 id 与各默认值。
	_, err = session.Execute(ctx, `INSERT INTO users (name) VALUES ('a')`)
	require.NoError(t, err)
	rows, err := session.Execute(ctx, `SELECT id, name, email, created_at IS NOT NULL, code FROM users`)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(1), "a", nil, int64(1), "A"}}, rows.Rows.Rows)
}

// 没有声明主键的建表请求里，自增列就是主键（SQLite 只有 INTEGER PRIMARY KEY 能自增）；多列主键、主键是另一列、
// 类型不是 INTEGER、自增列带默认值、降序的自增主键都以错误拒绝，而不是写出不自增的表。
func TestCreateTableAutoIncrementIsTheIntegerPrimaryKey(t *testing.T) {
	session := newDatabase(t)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "integer", AutoIncrement: true}, {Name: "n", Type: "TEXT", Nullable: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE \"t\" (\n  \"id\" integer PRIMARY KEY NOT NULL,\n  \"n\" TEXT\n)"}, statements)

	id := dbdriver.NewColumn{Name: "id", Type: "INTEGER", AutoIncrement: true}
	other := dbdriver.NewColumn{Name: "other", Type: "INTEGER"}
	primary := func(columns ...dbdriver.IndexColumn) dbdriver.IndexDefinition {
		return dbdriver.IndexDefinition{Name: "pk", Type: dbdriver.IndexTypePrimary, Columns: columns}
	}
	for name, req := range map[string]dbdriver.CreateTableRequest{
		"not INTEGER":            {Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "BIGINT", AutoIncrement: true}}},
		"with a default":         {Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "INTEGER", AutoIncrement: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "1"}}},
		"primary key is another": {Name: "t", Columns: []dbdriver.NewColumn{id, other}, Indexes: []dbdriver.IndexDefinition{primary(column("other"))}},
		"multi-column primary":   {Name: "t", Columns: []dbdriver.NewColumn{id, other}, Indexes: []dbdriver.IndexDefinition{primary(column("id"), column("other"))}},
		"descending primary":     {Name: "t", Columns: []dbdriver.NewColumn{id}, Indexes: []dbdriver.IndexDefinition{primary(dbdriver.IndexColumn{Name: "id", Descending: true})}},
		"two auto-increments":    {Name: "t", Columns: []dbdriver.NewColumn{id, {Name: "id2", Type: "INTEGER", AutoIncrement: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := session.CreateTableStatements(ctx, "", req)
			assert.Error(t, err)
		})
	}
}

// 不自增的主键写成表约束（可多列、可降序）；SQLite 不保存主键约束的名字，不写它。SQLite 没有注释：带注释的
// 建表请求以 ErrUnsupported 拒绝。
func TestCreateTablePrimaryKeyConstraintAndComments(t *testing.T) {
	session := newDatabase(t)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "main", dbdriver.CreateTableRequest{
		Name:    "pairs",
		Columns: []dbdriver.NewColumn{{Name: "a", Type: "TEXT"}, {Name: "b", Type: "INT"}},
		Indexes: []dbdriver.IndexDefinition{{Name: "pk_pairs", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{column("a"), {Name: "b", Descending: true}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE \"main\".\"pairs\" (\n  \"a\" TEXT NOT NULL,\n  \"b\" INT NOT NULL,\n  PRIMARY KEY (\"a\", \"b\" DESC)\n)"}, statements)
	runAtomic(t, session, "pairs", statements)
	assert.Equal(t, []dbdriver.IndexDefinition{{
		Name: "sqlite_autoindex_pairs_1", Type: dbdriver.IndexTypePrimary,
		Columns: []dbdriver.IndexColumn{column("a"), {Name: "b", Descending: true}}, Undroppable: true,
	}}, model(t, session, "pairs").Indexes)

	for name, req := range map[string]dbdriver.CreateTableRequest{
		"table comment":  {Name: "t", Comment: "x", Columns: []dbdriver.NewColumn{{Name: "a", Type: "TEXT"}}},
		"column comment": {Name: "t", Columns: []dbdriver.NewColumn{{Name: "a", Type: "TEXT", Comment: "x"}}},
	} {
		_, err := session.CreateTableStatements(ctx, "", req)
		assert.ErrorIs(t, err, dbdriver.ErrUnsupported, name)
	}
}

// 已有表的默认值写法照 pragma_table_info 的 dflt_value 判定：字符串常量（成对的单引号还原）与数字是值，NULL 是
// NULL，其余（CURRENT_TIMESTAMP、括号里的表达式——SQLite 存的是括号里面的部分、TRUE、blob）是表达式。
func TestAlterTableModelClassifiesDefaults(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE d (
		a TEXT, b TEXT DEFAULT 'x''y', c TEXT DEFAULT '', e INT DEFAULT 5, f REAL DEFAULT -1.5, g TEXT DEFAULT NULL,
		h TEXT DEFAULT CURRENT_TIMESTAMP, i TEXT DEFAULT (lower('X')), j INT DEFAULT TRUE, k BLOB DEFAULT X'00ff'
	)`)

	got := map[string][2]string{}
	for _, c := range model(t, session, "d").Columns {
		got[c.Name] = [2]string{string(c.DefaultKind), c.DefaultValue}
	}
	assert.Equal(t, map[string][2]string{
		"a": {"none", ""}, "b": {"value", "x'y"}, "c": {"value", ""}, "e": {"value", "5"}, "f": {"value", "-1.5"},
		"g": {"null", ""}, "h": {"expression", "CURRENT_TIMESTAMP"}, "i": {"expression", "lower('X')"},
		"j": {"expression", "TRUE"}, "k": {"expression", "X'00ff'"},
	}, got)
}

// 改表：加一列并在它上面建索引，删一列连带它的索引，给索引用到的列改名（索引跟着改名、不重建），改一个索引（删后
// 重建），删一个索引。删索引在列的改动之前，建索引在之后；整批原子执行后读回文件确认结构。
func TestAlterTableIndexesOnARealSQLiteDatabase(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, created_at TEXT, score REAL)`,
		`CREATE INDEX idx_users_name ON users (name)`,
		`CREATE UNIQUE INDEX idx_users_email ON users (email)`,
		`CREATE INDEX idx_users_created ON users (created_at DESC)`,
		`CREATE INDEX idx_users_score ON users (score)`,
		`INSERT INTO users (name, email, created_at, score) VALUES ('a', 'a@x', '2026', 1)`,
	)
	ctx := context.Background()

	req := keepAll(model(t, session, "users"))
	// 列：name 改名 full_name，删 created_at，加 status。
	req.Columns[1].Name = "full_name"
	req.Columns = append(req.Columns[:3], req.Columns[4])
	req.Drops = []string{"created_at"}
	req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "status", Type: "TEXT", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "new"})
	// 索引：idx_users_created 随列删除（请求没列出），删 idx_users_score，idx_users_email 改成降序的普通索引，
	// idx_users_name 照旧（列改名由 SQLite 带过去），新建 idx_users_status。
	indexes := make([]dbdriver.AlterIndex, 0, len(req.Indexes)+1)
	for _, index := range req.Indexes {
		switch index.OriginalName {
		case "idx_users_created", "idx_users_score":
			continue
		case "idx_users_email":
			index.Type = dbdriver.IndexTypeNormal
			index.Columns = []dbdriver.IndexColumn{{Name: "email", Descending: true}}
		case "idx_users_name":
			index.Columns = []dbdriver.IndexColumn{column("full_name")} // 写改名之后的列名（设计器就这样交）
		}
		indexes = append(indexes, index)
	}
	req.Indexes = append(indexes, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "idx_users_status", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("status")},
	}})
	req.IndexDrops = []string{"idx_users_score"}

	result, err := session.AlterTableStatements(ctx, "main", "users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`DROP INDEX "main"."idx_users_score"`,
		`DROP INDEX "main"."idx_users_created"`,
		`DROP INDEX "main"."idx_users_email"`,
		`ALTER TABLE "main"."users" DROP COLUMN "created_at"`,
		`ALTER TABLE "main"."users" RENAME COLUMN "name" TO "full_name"`,
		`ALTER TABLE "main"."users" ADD COLUMN "status" TEXT DEFAULT 'new'`,
		`CREATE INDEX "main"."idx_users_email" ON "users" ("email" DESC)`,
		`CREATE INDEX "main"."idx_users_status" ON "users" ("status")`,
	}, result.Statements)
	runAtomic(t, session, "users", result.Statements)

	after := model(t, session, "users")
	names := make([]string, len(after.Columns))
	for i, c := range after.Columns {
		names[i] = c.Name
	}
	assert.Equal(t, []string{"id", "full_name", "email", "score", "status"}, names)
	assert.Equal(t, []dbdriver.IndexDefinition{
		{Name: rowidPrimaryKeyName, Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{column("id")}, Undroppable: true},
		{Name: "idx_users_email", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "email", Descending: true}}},
		{Name: "idx_users_name", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("full_name")}},
		{Name: "idx_users_status", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("status")}},
	}, after.Indexes)
}

// 整批原子执行：最后一条失败时整批回滚，库文件里的表结构与执行前一样。
func TestAtomicAlterBatchWithAFailingStatementLeavesTheSchemaUnchanged(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, gone TEXT)`,
		`CREATE INDEX idx_users_gone ON users (gone)`,
	)
	ctx := context.Background()
	before := model(t, session, "users")

	req := keepAll(before)
	req.Columns = append(req.Columns[:2], dbdriver.AlterColumn{Name: "added", Type: "TEXT", Nullable: true})
	req.Indexes = append(req.Indexes[:1], dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "idx_users_added", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{column("added")},
	}})
	req.Drops = []string{"gone"}
	result, err := session.AlterTableStatements(ctx, "main", "users", req)
	require.NoError(t, err)
	require.Len(t, result.Statements, 4)

	batch, err := session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements: append(result.Statements, "INSERT INTO no_such_table VALUES (1)"), Atomic: true, Database: "main", Table: "users",
	})
	require.NoError(t, err)
	assert.True(t, batch.RolledBack)
	assert.Equal(t, before, model(t, session, "users"))
}

// 设计器表示不完整的已有索引只读（表达式、部分、指定排序规则），只能删除；UNIQUE 约束自动建的索引与主键不能删；
// 主键不可改。
func TestReadOnlyAndUndroppableIndexes(t *testing.T) {
	setup := []string{
		`CREATE TABLE t (code TEXT PRIMARY KEY, a TEXT UNIQUE, b TEXT, c TEXT COLLATE NOCASE)`,
		`CREATE INDEX ix_expr ON t (lower(a), b DESC)`,
		`CREATE INDEX ix_partial ON t (b) WHERE b IS NOT NULL`,
		`CREATE INDEX ix_collate ON t (b COLLATE NOCASE)`,
		// 列自己的排序规则不是索引写的：可以原样重建。
		`CREATE INDEX ix_column_collation ON t (c)`,
	}
	session := newDatabase(t, setup...)
	ctx := context.Background()

	m := model(t, session, "t")
	assert.Equal(t, []dbdriver.IndexDefinition{
		{Name: "sqlite_autoindex_t_1", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{column("code")}, Undroppable: true},
		{Name: "ix_collate", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("b")}, ReadOnlyReason: readOnlyCollation},
		{Name: "ix_column_collation", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("c")}},
		{Name: "ix_expr", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "lower(a)", Expression: true}, {Name: "b", Descending: true}}, ReadOnlyReason: readOnlyExpression},
		{Name: "ix_partial", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("b")}, ReadOnlyReason: "It is a partial index (WHERE b IS NOT NULL), which the designer cannot recreate."},
		{Name: "sqlite_autoindex_t_2", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{column("a")}, Undroppable: true},
	}, m.Indexes)

	without := func(name string) dbdriver.AlterTableRequest {
		req := keepAll(m)
		for i, index := range req.Indexes {
			if index.OriginalName == name {
				req.Indexes = append(req.Indexes[:i:i], req.Indexes[i+1:]...)
				break
			}
		}
		return req
	}
	changed := func(name string, change func(*dbdriver.AlterIndex)) dbdriver.AlterTableRequest {
		req := keepAll(m)
		for i := range req.Indexes {
			if req.Indexes[i].OriginalName == name {
				change(&req.Indexes[i])
			}
		}
		return req
	}

	// 只读的可以删除。
	for _, name := range []string{"ix_expr", "ix_partial", "ix_collate"} {
		req := without(name)
		req.IndexDrops = []string{name}
		result, err := session.AlterTableStatements(ctx, "main", "t", req)
		require.NoError(t, err, name)
		assert.Equal(t, []string{`DROP INDEX "main".` + quoteIdent(name)}, result.Statements)
	}
	// 自动索引与主键删不得，只读的与主键改不得。
	for name, req := range map[string]dbdriver.AlterTableRequest{
		"drop the UNIQUE constraint's index": func() dbdriver.AlterTableRequest {
			req := without("sqlite_autoindex_t_2")
			req.IndexDrops = []string{"sqlite_autoindex_t_2"}
			return req
		}(),
		"drop the primary key": func() dbdriver.AlterTableRequest {
			req := without("sqlite_autoindex_t_1")
			req.IndexDrops = []string{"sqlite_autoindex_t_1"}
			return req
		}(),
		"change the primary key": changed("sqlite_autoindex_t_1", func(i *dbdriver.AlterIndex) {
			i.Columns = []dbdriver.IndexColumn{column("a")}
		}),
		"change an expression index": changed("ix_expr", func(i *dbdriver.AlterIndex) { i.Type = dbdriver.IndexTypeUnique }),
		"add a primary key": func() dbdriver.AlterTableRequest {
			req := keepAll(m)
			req.Indexes = append(req.Indexes, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
				Name: "pk2", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{column("b")},
			}})
			return req
		}(),
	} {
		_, err := session.AlterTableStatements(ctx, "main", "t", req)
		assert.ErrorIs(t, err, dbdriver.ErrUnsupported, name)
	}
}

// SQLite 的 ADD COLUMN 不能加自增列（INTEGER PRIMARY KEY），也不能在有行的表上加默认值不是常量的列：语句生成
// 时就报错，不写出执行时才失败的语句。空表上非常量的默认值 SQLite 照样接受。
func TestAlterTableRefusesWhatAddColumnCannotDo(t *testing.T) {
	ctx := context.Background()
	add := func(session dbdriver.Session, added dbdriver.AlterColumn) (*dbdriver.AlterTableStatements, error) {
		req := keepAll(model(t, session, "t"))
		req.Columns = append(req.Columns, added)
		return session.AlterTableStatements(ctx, "main", "t", req)
	}
	timestamp := dbdriver.AlterColumn{Name: "at", Type: "TEXT", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "CURRENT_TIMESTAMP"}

	filled := newDatabase(t, `CREATE TABLE t (id INTEGER)`, `INSERT INTO t VALUES (1)`)
	_, err := add(filled, dbdriver.AlterColumn{Name: "n", Type: "INTEGER", AutoIncrement: true})
	assert.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = add(filled, timestamp)
	assert.ErrorContains(t, err, "constant")
	// 常量表达式（数字、字符串、加括号的）照样能加。
	for _, constant := range []string{"1", "(-1)", "'a'", "NULL", "X'00'"} {
		result, err := add(filled, dbdriver.AlterColumn{Name: "c", Type: "TEXT", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: constant})
		require.NoError(t, err, constant)
		_, err = filled.Execute(ctx, result.Statements[0])
		require.NoError(t, err, constant)
		_, err = filled.Execute(ctx, "ALTER TABLE t DROP COLUMN c")
		require.NoError(t, err)
	}

	empty := newDatabase(t, `CREATE TABLE t (id INTEGER)`)
	result, err := add(empty, timestamp)
	require.NoError(t, err)
	runAtomic(t, empty, "t", result.Statements)
}

// 外键带 ON UPDATE / ON DELETE；表达式索引在对象浏览里以表达式原文作列。
func TestTableMetadataReadsForeignKeyActionsAndExpressionIndexes(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id) ON DELETE CASCADE ON UPDATE SET NULL, code TEXT)`,
		`CREATE INDEX ix_orders_code ON orders (upper(code), id DESC)`,
	)

	meta, err := session.TableMetadata(context.Background(), "main", "orders")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{{Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id", OnUpdate: "SET NULL", OnDelete: "CASCADE"}}, meta.ForeignKeys)
	assert.Equal(t, []dbdriver.IndexMeta{{Name: "ix_orders_code", Columns: []string{"upper(code)", "id"}}}, meta.Indexes)
}

// 索引语句按表引用里的库名限定索引名，表名不带库名（附加库也一样）。
func TestIndexStatementsQualifyTheIndexNotTheTable(t *testing.T) {
	index := dbdriver.IndexDefinition{Name: `we"ird`, Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "a", Descending: true}}}
	for ref, want := range map[string][2]string{
		`"aux"."t"`:    {`CREATE UNIQUE INDEX "aux"."we""ird" ON "t" ("a" DESC)`, `DROP INDEX "aux"."we""ird"`},
		`"t"`:          {`CREATE UNIQUE INDEX "we""ird" ON "t" ("a" DESC)`, `DROP INDEX "we""ird"`},
		`"a""b"."c.d"`: {`CREATE UNIQUE INDEX "a""b"."we""ird" ON "c.d" ("a" DESC)`, `DROP INDEX "a""b"."we""ird"`},
		`"x.y"`:        {`CREATE UNIQUE INDEX "we""ird" ON "x.y" ("a" DESC)`, `DROP INDEX "we""ird"`},
	} {
		create, err := dialect{}.IndexSQL(ref, index)
		require.NoError(t, err)
		drop, err := dialect{}.DropIndexSQL(ref, index.Name)
		require.NoError(t, err)
		assert.Equal(t, want, [2]string{create, drop}, ref)
	}
	_, err := dialect{}.IndexSQL(`"t"`, dbdriver.IndexDefinition{Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{column("a")}})
	assert.Error(t, err, "SQLite needs an index name")
}

// 建索引的语句读不全（如注释里有括号）时，读结构不因它失败：索引按 pragma 给出的键列显示，标为只读、只能删除。
func TestAnIndexWhoseStatementCannotBeReadIsReadOnly(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE t (a TEXT, c TEXT)`,
		"CREATE INDEX ix ON t (a /* note ) */, c)",
	)
	ctx := context.Background()
	meta, err := session.TableMetadata(ctx, "main", "t")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.IndexMeta{{Name: "ix", Columns: []string{"a", "c"}}}, meta.Indexes)

	m, err := session.AlterTableModel(ctx, "main", "t")
	require.NoError(t, err)
	require.Len(t, m.Indexes, 1)
	assert.Equal(t, []dbdriver.IndexColumn{{Name: "a"}, {Name: "c"}}, m.Indexes[0].Columns)
	assert.Equal(t, readOnlyUnreadable, m.Indexes[0].ReadOnlyReason)
}

// 删掉一列再加回同名的新列、把另一列改成被删列的名字、改名之后加回旧名字的新列：都在真实的库上执行成功，
// 读回的列就是请求改完的样子。
func TestAlterTableReusesTheNamesOfDroppedAndRenamedColumns(t *testing.T) {
	for name, edit := range map[string]func(req *dbdriver.AlterTableRequest) []string{
		"drop and add back": func(req *dbdriver.AlterTableRequest) []string {
			req.Columns = append(req.Columns[:1], req.Columns[2:]...)
			req.Drops = []string{"name"}
			req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "name", Type: "INTEGER", Nullable: true})
			return []string{"id", "email", "note", "name"}
		},
		"rename into a dropped name": func(req *dbdriver.AlterTableRequest) []string {
			req.Columns = append(req.Columns[:1], req.Columns[2:]...)
			req.Drops = []string{"name"}
			req.Columns[1].Name = "name" // email → name
			return []string{"id", "name", "note"}
		},
		"rename then add the old name": func(req *dbdriver.AlterTableRequest) []string {
			req.Columns[1].Name = "full_name" // name → full_name
			req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "name", Type: "TEXT", Nullable: true})
			return []string{"id", "full_name", "email", "note", "name"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			session := newDatabase(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, note TEXT)`)
			req := keepAll(model(t, session, "users"))
			want := edit(&req)
			result, err := session.AlterTableStatements(context.Background(), "main", "users", req)
			require.NoError(t, err)
			runAtomic(t, session, "users", result.Statements)

			after := model(t, session, "users")
			names := make([]string, len(after.Columns))
			for i, c := range after.Columns {
				names[i] = c.Name
			}
			assert.Equal(t, want, names)
		})
	}
}
