// Package pwshlex 是 PowerShell 的词法基础：注释、引号、here-string 与括号的识别，以及整串的
// 闭合检查。命令策略的子命令拆分（internal/ai/policy）与送审前的反混淆判断（internal/pkg/pwshdecode）
// 共用这一份，两边对"哪里是注释、哪里是字符串"的认定必须一致。
//
// 这里的规则都按 PowerShell 自己的解析器（System.Management.Automation.Language.Parser）核对过：
//   - # 只在词首才开始注释（a#b 是一个词），前面是空白或 ; | & ( ) { } , 时才算词首；
//   - 单独的 CR 和 LF 一样是换行；
//   - PowerShell 把弯引号 ‘ ’ ‚ ‛ / “ ” „ 当作引号。本包遇到它们直接报错（fail-closed），
//     不去模拟它们和 ASCII 引号混用时的配对。
package pwshlex

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsAltQuote 报告 r 是不是 PowerShell 当作引号的非 ASCII 字符。
func IsAltQuote(r rune) bool {
	switch r {
	case '\u2018', '\u2019', '\u201A', '\u201B', '\u201C', '\u201D', '\u201E':
		return true
	}
	return false
}

// CommentStartsAt 报告 s[i] 处的 # 是否开始一条行注释：# 必须在词首。
func CommentStartsAt(s string, i int) bool {
	if s[i] != '#' {
		return false
	}
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	if unicode.IsSpace(r) {
		return true
	}
	switch r {
	case ';', '|', '&', '(', ')', '{', '}', ',':
		return true
	}
	return false
}

// LineCommentEnd 返回从 i（一条行注释的 #）起，注释最后一个字节的下标：注释到 CR 或 LF 为止。
func LineCommentEnd(s string, i int) int {
	for i+1 < len(s) && s[i+1] != '\n' && s[i+1] != '\r' {
		i++
	}
	return i
}

// BlockCommentEnd 在 s[i:] 以 <# 开头时返回 #> 中 > 的下标；未闭合返回 ok=false。
func BlockCommentEnd(s string, i int) (end int, ok bool) {
	idx := strings.Index(s[i+2:], "#>")
	if idx < 0 {
		return 0, false
	}
	return i + 2 + idx + 1, true
}

// HereStringAt 报告 s[i:] 是否以 here-string 开头（@' 或 @" 后到行尾只有空白）；是则返回引号与
// 内容开始的下标。
func HereStringAt(s string, i int) (quote byte, bodyStart int, ok bool) {
	if s[i] != '@' || i+1 >= len(s) || (s[i+1] != '\'' && s[i+1] != '"') {
		return 0, 0, false
	}
	j := i + 2
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j < len(s) && s[j] == '\r' {
		j++
	}
	if j < len(s) && s[j] == '\n' {
		return s[i+1], j + 1, true
	}
	return 0, 0, false
}

// HereStringEnd 从 bodyStart 起找 here-string 的结束标记（行首的 '@ 或 "@），返回 @ 的下标。
func HereStringEnd(s string, bodyStart int, quote byte) (int, bool) {
	for i := bodyStart; i < len(s); {
		if i+1 < len(s) && s[i] == quote && s[i+1] == '@' {
			return i + 1, true
		}
		nl := strings.IndexByte(s[i:], '\n')
		if nl < 0 {
			break
		}
		i += nl + 1
	}
	return 0, false
}

// SingleQuoteEnd 返回从 start（一个 '）起的单引号字符串结束处 ' 的下标；连写两个单引号是转义的引号。
func SingleQuoteEnd(s string, start int) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			return i, true
		}
	}
	return 0, false
}

// DoubleQuoteEnd 返回从 start（一个 "）起的双引号字符串结束处 " 的下标。` 转义下一个字符，
// "" 是转义的引号，$( … ) 子表达式里的引号与括号按代码处理。
func DoubleQuoteEnd(s string, start int) (int, error) {
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '`':
			i++
		case '"':
			if i+1 < len(s) && s[i+1] == '"' {
				i++
				continue
			}
			return i, nil
		case '$':
			if i+1 < len(s) && s[i+1] == '(' {
				end, err := MatchingClose(s, i+1)
				if err != nil {
					return 0, err
				}
				i = end
			}
		}
	}
	return 0, fmt.Errorf("unclosed double quote")
}

// MatchingClose 返回 s[open]（( { [ 之一）对应的闭合括号下标，跳过其中的注释、字符串与
// here-string。
func MatchingClose(s string, open int) (int, error) {
	var closeCh byte
	switch s[open] {
	case '(':
		closeCh = ')'
	case '{':
		closeCh = '}'
	case '[':
		closeCh = ']'
	default:
		return 0, fmt.Errorf("invalid delimiter %q", s[open])
	}
	stack := []byte{closeCh}
	for i := open + 1; i < len(s); i++ {
		next, err := SkipNonCode(s, i)
		if err != nil {
			return 0, err
		}
		if next != i {
			i = next
			continue
		}
		switch ch := s[i]; ch {
		case '(':
			stack = append(stack, ')')
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case ')', '}', ']':
			if stack[len(stack)-1] != ch {
				return 0, fmt.Errorf("unexpected %q", ch)
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unclosed %q", s[open])
}

// SkipNonCode 在 s[i] 开始一段注释、字符串、here-string 或 ` 转义时返回这段的最后一个字节下标，
// 否则原样返回 i。
func SkipNonCode(s string, i int) (int, error) {
	switch ch := s[i]; {
	case ch == '<' && i+1 < len(s) && s[i+1] == '#':
		end, ok := BlockCommentEnd(s, i)
		if !ok {
			return 0, fmt.Errorf("unclosed PowerShell block comment <#")
		}
		return end, nil
	case ch == '#' && CommentStartsAt(s, i):
		return LineCommentEnd(s, i), nil
	case ch == '@':
		if q, body, ok := HereStringAt(s, i); ok {
			end, ok := HereStringEnd(s, body, q)
			if !ok {
				return 0, fmt.Errorf("unclosed PowerShell here-string @%c", q)
			}
			return end, nil
		}
	case ch == '\'':
		end, ok := SingleQuoteEnd(s, i)
		if !ok {
			return 0, fmt.Errorf("unclosed single quote")
		}
		return end, nil
	case ch == '"':
		return DoubleQuoteEnd(s, i)
	case ch == '`':
		if i+1 < len(s) {
			return i + 1, nil
		}
	}
	return i, nil
}

// CheckSyntax 检查整串的引号、注释、here-string 与括号是否闭合，并拒绝弯引号。
func CheckSyntax(s string) error {
	if i := strings.IndexFunc(s, IsAltQuote); i >= 0 {
		r, _ := utf8.DecodeRuneInString(s[i:])
		return fmt.Errorf("PowerShell treats %q as a quote; use ASCII quotes", r)
	}
	var stack []byte
	for i := 0; i < len(s); i++ {
		next, err := SkipNonCode(s, i)
		if err != nil {
			return err
		}
		if next != i {
			i = next
			continue
		}
		switch ch := s[i]; ch {
		case '(':
			stack = append(stack, ')')
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case ')', '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return fmt.Errorf("unexpected %q", ch)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("unclosed bracket, expecting %q", stack[len(stack)-1])
	}
	return nil
}
