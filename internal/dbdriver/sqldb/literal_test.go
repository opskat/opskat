package sqldb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 没有任何方言规则时，值照改动前前端的写法：NULL 不加引号，其余（数字、布尔、日期文本）都
// 写成单引号成对转义的字符串字面量，数字照表格显示它的样子。
func TestLiteralWithoutDialectRulesQuotesEverythingAsText(t *testing.T) {
	for want, value := range map[string]any{
		"NULL":                  nil,
		"'42'":                  float64(42),
		"'1.5'":                 1.5,
		"'1e+21'":               1e21,
		"'true'":                true,
		"'O''Reilly'":           "O'Reilly",
		"'2026-04-03 05:06:07'": "2026-04-03 05:06:07",
		`'C:\new'`:              `C:\new`,
		"''":                    "",
		"'中文'":                  "中文",
	} {
		got, err := Literals{}.Literal(value, "varchar(10)")
		require.NoError(t, err)
		assert.Equal(t, want, got, "value %#v", value)
	}
}

// 反斜杠是转义符的方言（MySQL 未开 NO_BACKSLASH_ESCAPES）：反斜杠成对写，读回的才是原文。
func TestLiteralEscapesBackslashesWhenTheyAreEscapes(t *testing.T) {
	got, err := Literals{BackslashEscapes: true}.Literal(`C:\new\table\x 'q'`, "text")
	require.NoError(t, err)
	assert.Equal(t, `'C:\\new\\table\\x ''q'''`, got)
}

// Unicode 文本列的值加 N 前缀；其他列与 NULL 不加。
func TestLiteralPrefixesUnicodeTextColumns(t *testing.T) {
	literals := Literals{UnicodeText: func(columnType string) bool { return columnType == "nvarchar" }}

	got, err := literals.Literal("中文Ünï", "nvarchar")
	require.NoError(t, err)
	assert.Equal(t, "N'中文Ünï'", got)

	got, err = literals.Literal("abc", "varchar")
	require.NoError(t, err)
	assert.Equal(t, "'abc'", got)

	got, err = literals.Literal(nil, "nvarchar")
	require.NoError(t, err)
	assert.Equal(t, "NULL", got)
}

// 二进制列写成方言的十六进制字面量：文本按它的字节，[]byte 原样；NULL 仍是 NULL。
func TestLiteralWritesBinaryColumnsAsHex(t *testing.T) {
	literals := Literals{Binary: func(columnType string) (string, string, bool) {
		return "0x", "", strings.EqualFold(columnType, "varbinary")
	}}

	got, err := literals.Literal("\xde\xad\xbe\xef", "VARBINARY")
	require.NoError(t, err)
	assert.Equal(t, "0xdeadbeef", got)

	got, err = literals.Literal([]byte("AB"), "varbinary")
	require.NoError(t, err)
	assert.Equal(t, "0x4142", got)

	got, err = literals.Literal("", "varbinary")
	require.NoError(t, err)
	assert.Equal(t, "0x", got)

	got, err = literals.Literal(nil, "varbinary")
	require.NoError(t, err)
	assert.Equal(t, "NULL", got)

	got, err = literals.Literal("AB", "varchar")
	require.NoError(t, err)
	assert.Equal(t, "'AB'", got)
}

func TestLiteralRejectsValuesThatAreNotCellValues(t *testing.T) {
	_, err := Literals{}.Literal(map[string]any{"a": 1}, "text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value")
}
