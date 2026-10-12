package authtmpl

import (
	"strconv"
	"strings"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokPlus
	tokDot
	tokComma
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string // for tokIdent: the identifier; for tokString: the decoded value
}

// lexExpr 把一个 `{{ }}` 内部的表达式源码切成 token 序列。
// 字符串字面量在这里就完成转义解码，未识别的转义序列在这里报错。
func lexExpr(src string) ([]token, *ParseError) {
	var toks []token
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '+':
			toks = append(toks, token{kind: tokPlus})
			i++
		case c == '.':
			toks = append(toks, token{kind: tokDot})
			i++
		case c == ',':
			toks = append(toks, token{kind: tokComma})
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen})
			i++
		case c == '"':
			val, consumed, err := lexString(src[i:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tokString, text: val})
			i += consumed
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, token{kind: tokIdent, text: src[i:j]})
			i = j
		default:
			return nil, newParseError(CodeUnexpectedCharacter, "unexpected character "+strconv.Quote(string(c)), "char", string(c))
		}
	}
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart 允许 `-`：扩展 configSchema 的属性名可以含它。语言里没有减法，
// 所以 `-` 只会是标识符的一部分，且不能开头。
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}

// lexString 解码从 s[0]=='"' 开始的一个字符串字面量，返回解码后的值、
// 消耗掉的字节数（含首尾引号），以及遇到未识别转义 / 未闭合引号时的错误。
// 转义与 Go 的双引号字符串字面量相同（扩展 auth 绑定原先用 strconv.Unquote
// 解码字面量，渲染出的字节不能变），其余转义报错。只由 lexExpr 在遇到 '"' 时调用。
func lexString(s string) (string, int, *ParseError) {
	var b strings.Builder
	i := 1
	n := len(s)
	for i < n {
		c := s[i]
		if c == '"' {
			return b.String(), i + 1, nil
		}
		if c == '\\' {
			if i+1 >= n {
				return "", 0, newParseError(CodeUnterminatedString, "unterminated escape sequence in string literal")
			}
			r, multibyte, tail, err := strconv.UnquoteChar(s[i:], '"')
			if err != nil {
				return "", 0, newParseError(CodeInvalidEscape, "invalid escape sequence \\"+string(s[i+1])+" in string literal", "char", string(s[i+1]))
			}
			// 与 strconv.Unquote 一致：\xNN 与八进制转义是一个原始字节，
			// \u / \U 是码点的 UTF-8 编码。
			if multibyte {
				b.WriteRune(r)
			} else {
				b.WriteByte(byte(r))
			}
			i = n - len(tail)
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", 0, newParseError(CodeUnterminatedString, "unterminated string literal")
}
