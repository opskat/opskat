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

// 方言声明要经函数转换的列类型，字符串字面量包进它的转换写法（Oracle 带本地时区的时间戳按
// TO_TIMESTAMP_TZ 读带时区的文本）；其他列、NULL 与写成十六进制的二进制列不包。
func TestLiteralWrapsConvertedColumns(t *testing.T) {
	literals := Literals{
		Converted: func(columnType string) (string, string, bool) {
			return "TO_TIMESTAMP_TZ(", ", 'YYYY-MM-DD HH24:MI:SS.FF TZH:TZM')", strings.Contains(columnType, "LOCAL TIME ZONE")
		},
		Binary: func(columnType string) (string, string, bool) {
			return "HEXTORAW('", "')", columnType == "RAW(16)"
		},
	}

	got, err := literals.Literal("2026-10-24 10:11:12.123456 +00:00", "TIMESTAMP(6) WITH LOCAL TIME ZONE")
	require.NoError(t, err)
	assert.Equal(t, "TO_TIMESTAMP_TZ('2026-10-24 10:11:12.123456 +00:00', 'YYYY-MM-DD HH24:MI:SS.FF TZH:TZM')", got)

	got, err = literals.Literal(nil, "TIMESTAMP(6) WITH LOCAL TIME ZONE")
	require.NoError(t, err)
	assert.Equal(t, "NULL", got)

	got, err = literals.Literal("2026-10-24 10:11:12", "TIMESTAMP(6)")
	require.NoError(t, err)
	assert.Equal(t, "'2026-10-24 10:11:12'", got)

	got, err = literals.Literal("AB", "RAW(16)")
	require.NoError(t, err)
	assert.Equal(t, "HEXTORAW('4142')", got)
}

func TestLiteralRejectsValuesThatAreNotCellValues(t *testing.T) {
	_, err := Literals{}.Literal(map[string]any{"a": 1}, "text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value")
}

// 方言按值给出自己的写法（SQL Server 把 16 字节的 uniqueidentifier 写成 GUID 文本）时，字面量就是它给的那个：
// 先于二进制、N 前缀与转换写法；字节值与文本都按它们的文本交给方言。NULL 不经过它。
func TestLiteralUsesTheDialectsFormattedValue(t *testing.T) {
	var asked []string
	literals := Literals{
		Formatted: func(columnType, text string) (string, bool) {
			asked = append(asked, columnType+":"+text)
			if columnType == "guid" && len(text) == 2 {
				return "'formatted'", true
			}
			return "", false
		},
		UnicodeText: func(string) bool { return true },
		Binary:      func(string) (string, string, bool) { return "0x", "", true },
		Converted:   func(string) (string, string, bool) { return "CONVERT(", ")", true },
	}

	got, err := literals.Literal([]byte("AB"), "guid")
	require.NoError(t, err)
	assert.Equal(t, "'formatted'", got)

	got, err = literals.Literal("AB", "guid")
	require.NoError(t, err)
	assert.Equal(t, "'formatted'", got)

	got, err = literals.Literal(nil, "guid")
	require.NoError(t, err)
	assert.Equal(t, "NULL", got)
	assert.Equal(t, []string{"guid:AB", "guid:AB"}, asked)
}

// 方言不认的值照原来的写法：二进制列写十六进制、文本列带 N 前缀与转换写法、其余加引号，与没有这个钩子时一样。
func TestLiteralFallsThroughWhenTheDialectDoesNotFormatTheValue(t *testing.T) {
	base := Literals{
		BackslashEscapes: true,
		UnicodeText:      func(columnType string) bool { return columnType == "nvarchar" },
		Binary: func(columnType string) (string, string, bool) {
			return "0x", "", columnType == "varbinary"
		},
		Converted: func(columnType string) (string, string, bool) {
			return "TO_TS(", ")", columnType == "ts"
		},
	}
	withHook := base
	withHook.Formatted = func(string, string) (string, bool) { return "", false }
	for _, c := range []struct {
		value      any
		columnType string
	}{
		{"\xde\xad", "varbinary"}, {[]byte("AB"), "varbinary"}, {"中文 'q' \\", "nvarchar"},
		{"2026-10-24", "ts"}, {float64(42), "int"}, {true, "bit"}, {"", "varchar"},
	} {
		want, err := base.Literal(c.value, c.columnType)
		require.NoError(t, err)
		got, err := withHook.Literal(c.value, c.columnType)
		require.NoError(t, err)
		assert.Equal(t, want, got, "value %#v in a %s column", c.value, c.columnType)
	}
}
