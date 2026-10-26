package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func newDatabase(t *testing.T, statements ...string) dbdriver.Session {
	t.Helper()
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "browse.db"),
	})
	for _, statement := range statements {
		_, err := session.Execute(context.Background(), statement)
		require.NoError(t, err, statement)
	}
	return session
}

func TestSQLitePassesBrowseConformance(t *testing.T) {
	d, err := dbregistry.Lookup(asset_entity.DriverSQLite)
	require.NoError(t, err)

	dbdrivertest.RunBrowseConformance(t, d, func(t *testing.T) dbdriver.Session {
		session, err := d.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "browse.db"),
		}})
		require.NoError(t, err)
		_, err = session.Execute(context.Background(), "CREATE TABLE t (id INTEGER PRIMARY KEY)")
		require.NoError(t, err)
		return session
	})
}

func TestListDatabasesShowsMain(t *testing.T) {
	session := newDatabase(t)

	databases, err := session.ListDatabases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"main"}, databases)
}

func TestListTablesListsTablesAndViewsWithoutInternalOnes(t *testing.T) {
	session := newDatabase(t,
		"CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT)",
		"CREATE TABLE orders (id INTEGER)",
		"CREATE VIEW v_orders AS SELECT id FROM orders",
	)
	ctx := context.Background()

	// 库名为空等同 main；sqlite_sequence 之类的内部表不列出。
	for _, database := range []string{"main", ""} {
		tables, err := session.ListTables(ctx, database)
		require.NoError(t, err)
		assert.Equal(t, []string{"orders", "users", "v_orders"}, tables, "database %q", database)
	}
}

func TestTableMetadata(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id),
			code TEXT
		)`,
		`CREATE UNIQUE INDEX idx_orders_code ON orders(code)`,
	)

	meta, err := session.TableMetadata(context.Background(), "main", "orders")
	require.NoError(t, err)

	require.Len(t, meta.Columns, 3)
	assert.Equal(t, "id", meta.Columns[0].Name)
	assert.True(t, meta.Columns[0].PrimaryKey)
	assert.Equal(t, "user_id", meta.Columns[1].Name)
	assert.False(t, meta.Columns[1].Nullable)
	assert.False(t, meta.Columns[1].PrimaryKey)
	assert.True(t, meta.Columns[2].Nullable)

	assert.Contains(t, meta.Indexes, dbdriver.IndexMeta{Name: "idx_orders_code", Columns: []string{"code"}, Unique: true})

	assert.Equal(t, []dbdriver.ForeignKeyMeta{{Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id"}}, meta.ForeignKeys)
}

// SQLite 没有列注释（恒为空串）；默认值 NULL（没有默认值）与空串默认值可区分。
func TestTableMetadataCommentAndDefault(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE d (id INTEGER PRIMARY KEY, a TEXT, b TEXT DEFAULT '', c TEXT DEFAULT 'x')`)

	meta, err := session.TableMetadata(context.Background(), "main", "d")
	require.NoError(t, err)

	byName := map[string]dbdriver.ColumnMeta{}
	for _, c := range meta.Columns {
		assert.Equal(t, "", c.Comment, c.Name)
		byName[c.Name] = c
	}
	assert.Nil(t, byName["a"].Default)
	require.NotNil(t, byName["b"].Default)
	assert.Equal(t, "''", *byName["b"].Default)
	require.NotNil(t, byName["c"].Default)
	assert.Equal(t, "'x'", *byName["c"].Default)
}

// 无外键的表，foreignKeys 序列化为 [] 而非 null：前端直接取 length。
func TestTableMetadataSerializesEmptyListsAsArrays(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE plain (id INTEGER PRIMARY KEY, name TEXT)`)

	meta, err := session.TableMetadata(context.Background(), "main", "plain")
	require.NoError(t, err)

	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"foreignKeys":[]`)
	assert.NotContains(t, string(raw), `"foreignKeys":null`)
}

func TestListObjectsAndObjectSource(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE t (id INTEGER PRIMARY KEY, n INTEGER)`,
		`CREATE VIEW v_t AS SELECT id FROM t`,
		`CREATE TRIGGER trg_t AFTER INSERT ON t BEGIN UPDATE t SET n = 1; END`,
	)
	ctx := context.Background()

	objects, err := session.ListObjects(ctx, "main")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "v_t", Type: "view"}}, objects.Views)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "trg_t", Type: "trigger"}}, objects.Triggers)
	assert.Empty(t, objects.Procedures)
	assert.Empty(t, objects.Functions)

	source, err := session.ObjectSource(ctx, "main", "view", "v_t", "")
	require.NoError(t, err)
	assert.Contains(t, source, "CREATE VIEW")
}

// SQLite 没有存储过程和函数：声明之外的对象类型被拒绝，且不查库。
func TestObjectSourceRefusesObjectTypesSQLiteDoesNotHave(t *testing.T) {
	session := newDatabase(t)

	for _, objType := range []string{"procedure", "function", "sequence"} {
		_, err := session.ObjectSource(context.Background(), "main", objType, "x", "")
		require.ErrorIs(t, err, dbdriver.ErrUnsupported, objType)
	}
}

func TestQuoteIdent(t *testing.T) {
	assert.Equal(t, `"user"`, quoteIdent("user"))
	assert.Equal(t, `"a""b"`, quoteIdent(`a"b`))
}

// 附加库的名字是 schema：对象按它限定，名字里的引号被转义。
func TestQualify(t *testing.T) {
	assert.Equal(t, `"main".sqlite_master`, qualify("main", "sqlite_master"))
	assert.Equal(t, `sqlite_master`, qualify("", "sqlite_master"))
	assert.Equal(t, `"we""ird".sqlite_master`, qualify(`we"ird`, "sqlite_master"))
}
