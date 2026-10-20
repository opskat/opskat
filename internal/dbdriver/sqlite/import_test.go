package sqlite

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func texts(values ...string) []dbdriver.ImportValue {
	out := make([]dbdriver.ImportValue, len(values))
	for i, v := range values {
		out[i] = dbdriver.ImportValue{Kind: dbdriver.ImportText, Text: v}
	}
	return out
}

// 追加/更新用 ON CONFLICT … DO UPDATE，追加/跳过与只映射了键的追加/更新用 INSERT OR IGNORE，没有
// MySQL 的写法；BLOB 列写成 X'…'。与改动前前端生成的文本逐字一致（原 tableImport.test.ts 的断言）。
func TestImportUpsertSkipAndBinaryForSQLite(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	generate := func(mode dbdriver.ImportMode, columns []string, row []dbdriver.ImportValue) []string {
		t.Helper()
		mock.ExpectQuery(columnsSQL + `"main".pragma_table_info(?)`).WithArgs("users").WillReturnRows(
			sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value"}).
				AddRow("id", "INTEGER", "NO", nil).AddRow("name", "TEXT", "YES", nil).AddRow("data", "BLOB", "YES", nil))
		statements, err := session.ImportStatements(ctx, "main", "users", dbdriver.ImportRequest{
			Mode: mode, Columns: columns, Keys: []string{"id"}, Rows: [][]dbdriver.ImportValue{row},
		})
		require.NoError(t, err)
		return statements
	}

	assert.Equal(t, []string{`INSERT INTO "main"."users" ("id", "name") VALUES ('1', 'Alice') ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name";`},
		generate(dbdriver.ImportAppendUpdate, []string{"id", "name"}, texts("1", "Alice")))
	assert.Equal(t, []string{`INSERT OR IGNORE INTO "main"."users" ("id", "name") VALUES ('1', 'Alice');`},
		generate(dbdriver.ImportAppendSkip, []string{"id", "name"}, texts("1", "Alice")))
	assert.Equal(t, []string{`INSERT OR IGNORE INTO "main"."users" ("id") VALUES ('1');`},
		generate(dbdriver.ImportAppendUpdate, []string{"id"}, texts("1")))
	assert.Equal(t, []string{`INSERT INTO "main"."users" ("id", "data") VALUES ('1', X'48656c6c6f');`},
		generate(dbdriver.ImportAppend, []string{"id", "data"}, []dbdriver.ImportValue{
			{Kind: dbdriver.ImportText, Text: "1"}, {Kind: dbdriver.ImportBinary, Hex: "48656c6c6f"},
		}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 在真实的 SQLite 上：追加/更新执行后，已有的行被更新、新的行被插入，二进制按字节读回。
func TestImportStatementsRoundTripOnARealDatabase(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, data BLOB)`, `INSERT INTO users VALUES (1, 'old', NULL)`)
	ctx := context.Background()

	statements, err := session.ImportStatements(ctx, "main", "users", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppendUpdate, Columns: []string{"id", "name", "data"}, Keys: []string{"id"},
		Rows: [][]dbdriver.ImportValue{
			{{Kind: dbdriver.ImportText, Text: "1"}, {Kind: dbdriver.ImportText, Text: "Alice"}, {Kind: dbdriver.ImportNull}},
			{{Kind: dbdriver.ImportText, Text: "2"}, {Kind: dbdriver.ImportText, Text: "Bob"}, {Kind: dbdriver.ImportBinary, Hex: "00ff41"}},
		},
		ExtendedInsert: true, MaxStatementSizeKB: 1024,
	})
	require.NoError(t, err)
	require.Len(t, statements, 1)
	_, err = session.Execute(ctx, statements[0])
	require.NoError(t, err)

	result, err := session.Execute(ctx, `SELECT id, name, hex(data) FROM users ORDER BY id`)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(1), "Alice", ""}, {int64(2), "Bob", "00FF41"}}, result.Rows.Rows)
}

// 列的类别按声明类型的亲和性判断，日期与日期时间按声明名。
func TestColumnCategoriesForSQLite(t *testing.T) {
	for columnType, want := range map[string]dbdriver.ColumnCategory{
		"INTEGER": dbdriver.CategoryNumber, "bigint unsigned": dbdriver.CategoryNumber, "REAL": dbdriver.CategoryNumber,
		"DECIMAL(10,2)": dbdriver.CategoryNumber, "DATE": dbdriver.CategoryDate, "DATETIME": dbdriver.CategoryDateTime,
		"timestamp": dbdriver.CategoryDateTime, "BLOB": dbdriver.CategoryBinary, "VARCHAR(20)": dbdriver.CategoryText,
		"TEXT": dbdriver.CategoryText, "": dbdriver.CategoryText,
	} {
		assert.Equal(t, want, dialect{}.ColumnCategory(columnType), columnType)
	}
}
