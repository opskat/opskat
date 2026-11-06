package mysql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

// 各导入模式的写法与改动前前端生成的文本逐字一致（原 tableImport.test.ts 的断言）：追加/更新用
// ON DUPLICATE KEY UPDATE，追加/跳过与只映射了键的追加/更新用 INSERT IGNORE，更新与删除按键定位，
// 复制先清空。值一律按文本加引号，NULL 照旧。
func TestImportModesForMySQL(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	generate := func(req dbdriver.ImportRequest) []string {
		t.Helper()
		expectColumns(mock, "`appdb`.`users`", [2]string{"id", "int(11)"}, [2]string{"name", "varchar(64)"}, [2]string{"email", "varchar(64)"})
		expectSQLMode(mock, "STRICT_TRANS_TABLES")
		statements, err := session.ImportStatements(ctx, "appdb", "users", req)
		require.NoError(t, err)
		return statements
	}
	request := func(mode dbdriver.ImportMode, columns ...string) dbdriver.ImportRequest {
		return dbdriver.ImportRequest{
			Mode: mode, Columns: columns, Keys: []string{"id"},
			Rows: [][]dbdriver.ImportValue{texts("1", "Alice", "alice@example.test")[:len(columns)]},
		}
	}

	assert.Equal(t, []string{"UPDATE `appdb`.`users` SET `name` = 'Alice', `email` = 'alice@example.test' WHERE `id` = '1';"},
		generate(request(dbdriver.ImportUpdate, "id", "name", "email")))
	assert.Equal(t, []string{
		"INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test') ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `email` = VALUES(`email`);",
	}, generate(request(dbdriver.ImportAppendUpdate, "id", "name", "email")))
	assert.Equal(t, []string{"INSERT IGNORE INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test');"},
		generate(request(dbdriver.ImportAppendSkip, "id", "name", "email")))
	assert.Equal(t, []string{"INSERT IGNORE INTO `appdb`.`users` (`id`) VALUES ('1');"},
		generate(request(dbdriver.ImportAppendUpdate, "id")))
	assert.Equal(t, []string{"DELETE FROM `appdb`.`users` WHERE `id` = '1';"},
		generate(request(dbdriver.ImportDelete, "id", "name", "email")))
	assert.Equal(t, []string{
		"DELETE FROM `appdb`.`users`;",
		"INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test');",
	}, generate(request(dbdriver.ImportCopy, "id", "name", "email")))

	// 空串与 NULL 是两个值。
	assert.Equal(t, []string{
		"INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', '', NULL);",
	}, generate(dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"id", "name", "email"},
		Rows: [][]dbdriver.ImportValue{{{Kind: dbdriver.ImportText, Text: "1"}, {Kind: dbdriver.ImportText}, {Kind: dbdriver.ImportNull}}},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 需要按键匹配的模式没有映射键列时不生成语句；更新模式没有可更新的列时同样不生成。
func TestImportWithoutKeysGeneratesNothingForMySQL(t *testing.T) {
	session, mock := newMock(t)
	for _, mode := range []dbdriver.ImportMode{dbdriver.ImportUpdate, dbdriver.ImportAppendUpdate, dbdriver.ImportAppendSkip, dbdriver.ImportDelete} {
		expectColumns(mock, "`appdb`.`users`", [2]string{"id", "int"}, [2]string{"name", "varchar(64)"})
		expectSQLMode(mock, "")
		statements, err := session.ImportStatements(context.Background(), "appdb", "users", dbdriver.ImportRequest{
			Mode: mode, Columns: []string{"name"}, Keys: []string{"id"}, Rows: [][]dbdriver.ImportValue{texts("Alice")},
		})
		require.NoError(t, err)
		assert.Empty(t, statements, mode)
	}
	expectColumns(mock, "`appdb`.`users`", [2]string{"id", "int"}, [2]string{"name", "varchar(64)"})
	expectSQLMode(mock, "")
	statements, err := session.ImportStatements(context.Background(), "appdb", "users", dbdriver.ImportRequest{
		Mode: dbdriver.ImportUpdate, Columns: []string{"id"}, Keys: []string{"id"}, Rows: [][]dbdriver.ImportValue{texts("1")},
	})
	require.NoError(t, err)
	assert.Empty(t, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 字面量按目标列与会话：反斜杠按 sql_mode 转义，按二进制解码出的字节写成 X'…'（与改动前一样），
// 没按二进制解码的文本即使写进二进制列也照字符串写。
func TestImportLiteralsForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectColumns(mock, "`appdb`.`files`", [2]string{"id", "int"}, [2]string{"path", "varchar(255)"}, [2]string{"payload", "blob"})
	expectSQLMode(mock, "STRICT_TRANS_TABLES")

	statements, err := session.ImportStatements(context.Background(), "appdb", "files", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"id", "path", "payload"},
		Rows: [][]dbdriver.ImportValue{{
			{Kind: dbdriver.ImportText, Text: "1"}, {Kind: dbdriver.ImportText, Text: `C:\new`}, {Kind: dbdriver.ImportBinary, Hex: "48656c6c6f"},
		}, {
			{Kind: dbdriver.ImportText, Text: "2"}, {Kind: dbdriver.ImportText, Text: "x"}, {Kind: dbdriver.ImportText, Text: "AB"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO `appdb`.`files` (`id`, `path`, `payload`) VALUES ('1', 'C:\\\\new', X'48656c6c6f');",
		"INSERT INTO `appdb`.`files` (`id`, `path`, `payload`) VALUES ('2', 'x', 'AB');",
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 上限可以带小数（对话框的输入框接受 1.5）：请求照前端发来的 JSON 解出，1.5 KB 按 1536 个字符算，
// 与改动前一致。
func TestImportExtendedInsertAcceptsAFractionalLimitForMySQL(t *testing.T) {
	session, mock := newMock(t)
	row := strings.Repeat("a", 700)
	var req dbdriver.ImportRequest
	require.NoError(t, json.Unmarshal([]byte(`{"mode":"append","columns":["body"],"keys":[],"extendedInsert":true,`+
		`"maxStatementSizeKb":1.5,"rows":[[{"kind":"text","text":"`+row+`"}],[{"kind":"text","text":"`+row+`"}]]}`), &req))
	expectColumns(mock, "`appdb`.`notes`", [2]string{"body", "text"})
	expectSQLMode(mock, "")

	statements, err := session.ImportStatements(context.Background(), "appdb", "notes", req)
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO `appdb`.`notes` (`body`) VALUES ('" + row + "'), ('" + row + "');"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 多行插入：按上限把行分成几条，每条不超过 MaxStatementSizeKB；一行本身就超过上限时单独成一条。
// 长度按字符（UTF-16 码元）计，与改动前一致。
func TestImportExtendedInsertChunksForMySQL(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	generate := func(rows ...string) []string {
		t.Helper()
		expectColumns(mock, "`appdb`.`notes`", [2]string{"body", "text"})
		expectSQLMode(mock, "")
		req := dbdriver.ImportRequest{Mode: dbdriver.ImportAppend, Columns: []string{"body"}, ExtendedInsert: true, MaxStatementSizeKB: 1}
		for _, row := range rows {
			req.Rows = append(req.Rows, texts(row))
		}
		statements, err := session.ImportStatements(ctx, "appdb", "notes", req)
		require.NoError(t, err)
		return statements
	}
	prefix := "INSERT INTO `appdb`.`notes` (`body`) VALUES "
	a, b, c := strings.Repeat("a", 450), strings.Repeat("b", 450), strings.Repeat("c", 2000)

	assert.Equal(t, []string{
		prefix + "('" + a + "'), ('" + b + "');",
		prefix + "('" + c + "');",
		prefix + "('x');",
	}, generate(a, b, c, "x"))

	zh := strings.Repeat("中", 300)
	assert.Equal(t, []string{prefix + "('" + zh + "'), ('" + zh + "'), ('" + zh + "');"}, generate(zh, zh, zh))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 列的类别按 SHOW FULL COLUMNS 的类型名判断（长度与 unsigned 等修饰不影响）。
func TestColumnCategoriesForMySQL(t *testing.T) {
	for columnType, want := range map[string]dbdriver.ColumnCategory{
		"int(11) unsigned": dbdriver.CategoryNumber, "tinyint(1)": dbdriver.CategoryNumber, "decimal(10,2)": dbdriver.CategoryNumber,
		"double": dbdriver.CategoryNumber, "date": dbdriver.CategoryDate, "datetime(6)": dbdriver.CategoryDateTime,
		"timestamp": dbdriver.CategoryDateTime, "varbinary(16)": dbdriver.CategoryBinary, "longblob": dbdriver.CategoryBinary,
		"varchar(64)": dbdriver.CategoryText, "time": dbdriver.CategoryText, "json": dbdriver.CategoryText,
	} {
		assert.Equal(t, want, dialect{}.ColumnCategory(columnType), columnType)
	}
}
