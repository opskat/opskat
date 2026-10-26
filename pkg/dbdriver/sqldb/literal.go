package sqldb

import (
	"encoding/hex"
	"regexp"
	"strings"
)

// Literals 是方言按目标列的类型写字面量的规则。写进表里的值（行语句等写路径生成的语句）都
// 经它写成字面量；筛选条件里的值与列类型无关，不经过它。零值是没有任何方言规则：所有非
// NULL 的值都写成单引号成对转义的字符串字面量。
type Literals struct {
	// BackslashEscapes 为真表示文本字面量里的反斜杠是转义符（MySQL 会话的 sql_mode 不含
	// NO_BACKSLASH_ESCAPES）：反斜杠要成对写，存进去的才是原文。
	BackslashEscapes bool
	// UnicodeText 报告这种列类型的文本要加 N 前缀（SQL Server 的 nchar / nvarchar / ntext）；
	// nil 表示方言没有这种写法。
	UnicodeText func(columnType string) bool
	// Binary 报告这种列类型是二进制，并给出十六进制字面量的前后缀（0x… / X'…' / '\x…'::bytea）；
	// nil 表示方言照文本写二进制列。
	Binary func(columnType string) (prefix, suffix string, ok bool)
	// BinaryBytesOnly 为真表示只有字节值（[]byte，如导入按二进制解码出的值）写成十六进制字面量；
	// 写进二进制列的文本（表格里带回的值、导入里没按二进制解码的值）照字符串写（MySQL 一直这样写）。
	BinaryBytesOnly bool
	// Converted 报告这种列类型的文本要经函数转换才写得进去，并给出包在字符串字面量前后的写法
	// （Oracle 带时区的时间戳：TO_TIMESTAMP_TZ('…', '…TZH:TZM')，否则本地时区的列不认表格显示的
	// 时区、没有小数秒的负时区会丢掉符号）；nil 表示方言由数据库按列的类型转换字符串。
	Converted func(columnType string) (prefix, suffix string, ok bool)
}

// Literal 把要写进 columnType 列的值写成字面量。nil 是 NULL；二进制列写成方言的十六进制字面量
// （文本按它的字节）；其余写成字符串字面量——数字、布尔、日期文本也一样，由数据库按列的类型
// 转换，数字照表格显示它的样子（见 valueText）；方言声明了 Converted 的列类型，字符串字面量再包进
// 它的转换写法。BinaryBytesOnly 时二进制列只有字节值写成十六进制。
// 列类型是驱动目录里给出的写法；不认识的类型（包括空串）照文本写。
func (l Literals) Literal(value any, columnType string) (string, error) {
	if value == nil {
		return "NULL", nil
	}
	var text string
	b, isBytes := value.([]byte)
	if isBytes {
		text = string(b)
	} else {
		var err error
		if text, err = valueText(value); err != nil {
			return "", err
		}
	}
	if l.Binary != nil && (isBytes || !l.BinaryBytesOnly) {
		if prefix, suffix, ok := l.Binary(columnType); ok {
			return prefix + hex.EncodeToString([]byte(text)) + suffix, nil
		}
	}
	if l.BackslashEscapes {
		text = strings.ReplaceAll(text, `\`, `\\`)
	}
	quoted := quoteString(text)
	if l.UnicodeText != nil && l.UnicodeText(columnType) {
		quoted = "N" + quoted
	}
	if l.Converted != nil {
		if prefix, suffix, ok := l.Converted(columnType); ok {
			return prefix + quoted + suffix, nil
		}
	}
	return quoted, nil
}

var (
	numberPattern    = regexp.MustCompile(`^[-+]?\d+(\.\d+)?$`)
	keywordPattern   = regexp.MustCompile(`(?i)^(true|false|null)$`)
	timestampPattern = regexp.MustCompile(`(?i)^(current_timestamp(\(\))?|now\(\))$`)
)

// Default 把用户在建表 / 改表对话框里填的默认值文本写成 DEFAULT 后面的内容：数字、TRUE / FALSE /
// NULL、CURRENT_TIMESTAMP / NOW() 原样写入，其余写成要存进 columnType 列的字面量（columnType 是
// 用户填的类型文本，按括号前的类型名判断写法）。这是两个对话框一直以来的规则。
func (l Literals) Default(value, columnType string) (string, error) {
	v := strings.TrimSpace(value)
	switch {
	case numberPattern.MatchString(v), timestampPattern.MatchString(v):
		return v, nil
	case keywordPattern.MatchString(v):
		return strings.ToUpper(v), nil
	}
	typeName, _, _ := strings.Cut(columnType, "(")
	return l.Literal(v, strings.TrimSpace(typeName))
}
