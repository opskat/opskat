// Package pwshdecode 判断一条 PowerShell 命令是否把要执行的内容藏在编码/解码后面
// （如 -EncodedCommand / FromBase64String + Invoke-Expression），导致模型审核器看不到实际
// 要执行的代码。纯算法包，不依赖 permission 或扩展运行时，与 internal/pkg/shelldecode 对称。
package pwshdecode

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MaxCommandBytes 与 shelldecode.MaxCommandBytes 保持一致：超过此长度不解析，按 too_long 失败。
const MaxCommandBytes = 16 * 1024

// ErrTooLong 在命令超过 MaxCommandBytes 时返回。
var ErrTooLong = errors.New("command exceeds max length")

// DecodesAndRuns 报告一条 PowerShell 命令是否既解码又执行代码（或通过 -EncodedCommand 直接
// 执行编码脚本）。超长返回 ErrTooLong；引号/注释/括号未闭合返回解析错误。
func DecodesAndRuns(command string) (bool, error) {
	if len(command) > MaxCommandBytes {
		return false, ErrTooLong
	}
	if err := checkSyntax(command); err != nil {
		return false, err
	}

	// 去掉注释并还原 PowerShell 反引号字符混淆（如 I`E`X、-E`nc），再转小写匹配。
	cleaned := strings.ToLower(stripCommentsAndBackticks(command))

	if hasEncodedCommandFlag(cleaned) {
		return true, nil
	}
	if hasDecoder(cleaned) && hasExecutor(cleaned) {
		return true, nil
	}
	return false, nil
}

// checkSyntax 检查 PowerShell 引号、块注释和括号/花括号闭合。
func checkSyntax(s string) error {
	var stack []byte
	n := len(s)
	for i := 0; i < n; i++ {
		ch := s[i]
		if ch == '<' && i+1 < n && s[i+1] == '#' {
			end := strings.Index(s[i+2:], "#>")
			if end < 0 {
				return fmt.Errorf("unclosed PowerShell block comment <#")
			}
			i = i + 2 + end + 1
			continue
		}
		if ch == '#' {
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '@' && i+1 < n && (s[i+1] == '\'' || s[i+1] == '"') {
			q := s[i+1]
			j := i + 2
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			if j < n && s[j] == '\n' {
				endIdx, ok := findHereEnd(s, j+1, q)
				if !ok {
					return fmt.Errorf("unclosed PowerShell here-string @%c", q)
				}
				i = endIdx
				continue
			}
		}
		if ch == '\'' {
			end, ok := scanSingle(s, i)
			if !ok {
				return fmt.Errorf("unclosed single quote")
			}
			i = end
			continue
		}
		if ch == '"' {
			end, ok := scanDouble(s, i)
			if !ok {
				return fmt.Errorf("unclosed double quote")
			}
			i = end
			continue
		}
		if ch == '`' {
			i++
			continue
		}
		switch ch {
		case '(', '{', '[':
			stack = append(stack, ch)
		case ')', '}', ']':
			if len(stack) == 0 || !matchDelim(stack[len(stack)-1], ch) {
				return fmt.Errorf("unexpected %q", ch)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("unclosed %q", stack[len(stack)-1])
	}
	return nil
}

func matchDelim(o, c byte) bool {
	return (o == '(' && c == ')') || (o == '{' && c == '}') || (o == '[' && c == ']')
}

func findHereEnd(s string, from int, q byte) (int, bool) {
	for i := from; i < len(s); {
		if i+1 < len(s) && s[i] == q && s[i+1] == '@' {
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

func scanSingle(s string, start int) (int, bool) {
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

func scanDouble(s string, start int) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		if s[i] == '`' {
			i++
			continue
		}
		if s[i] == '"' {
			return i, true
		}
	}
	return 0, false
}

// stripCommentsAndBackticks 去掉注释并展开非引号内的反引号混淆（如 I`E`X → IEX）。
func stripCommentsAndBackticks(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := len(s)
	for i := 0; i < n; i++ {
		ch := s[i]
		if ch == '<' && i+1 < n && s[i+1] == '#' {
			if end := strings.Index(s[i+2:], "#>"); end >= 0 {
				i = i + 2 + end + 1
				b.WriteByte(' ')
				continue
			}
		}
		if ch == '#' {
			for i < n && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
			continue
		}
		if ch == '\'' {
			if end, ok := scanSingle(s, i); ok {
				b.WriteString(s[i : end+1])
				i = end
				continue
			}
		}
		if ch == '`' {
			// PowerShell 中反引号用于转义或拆字混淆（`I`E`X），直接跳过反引号保留后一字符
			if i+1 < n && s[i+1] != '\n' && s[i+1] != '\r' {
				i++
				b.WriteByte(s[i])
				continue
			}
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// hasEncodedCommandFlag 检测 powershell / pwsh 的 -EncodedCommand 及其缩写（-ec、-enc、-e 等）。
func hasEncodedCommandFlag(lower string) bool {
	tokens := splitLooseTokens(lower)
	seenShell := false
	for i, tok := range tokens {
		base := stripExePath(tok)
		if base == "powershell" || base == "pwsh" {
			seenShell = true
			continue
		}
		if !seenShell {
			continue
		}
		if isEncodedFlagToken(tok) {
			// 后面必须有参数或内联值（-enc:xxx），避免把孤立字面量误判
			if strings.ContainsAny(tok, ":=") || i+1 < len(tokens) {
				return true
			}
		}
	}
	return false
}

func isEncodedFlagToken(tok string) bool {
	if len(tok) < 2 || (tok[0] != '-' && tok[0] != '/') {
		return false
	}
	flag := tok[1:]
	if idx := strings.IndexAny(flag, ":="); idx >= 0 {
		flag = flag[:idx]
	}
	if flag == "ec" || flag == "e" {
		return true
	}
	const full = "encodedcommand"
	return len(flag) >= 2 && strings.HasPrefix(full, flag)
}

func stripExePath(tok string) string {
	tok = strings.Trim(tok, `"'`)
	if idx := strings.LastIndexAny(tok, `/\`); idx >= 0 {
		tok = tok[idx+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

func hasDecoder(lower string) bool {
	decoderSubstrings := []string{
		"frombase64string",
		"fromhexstring",
		"gzipstream",
		"deflatestream",
		"certutil",
		"base64",
	}
	for _, sub := range decoderSubstrings {
		if containsIdentifierOrCall(lower, sub) {
			return true
		}
	}
	// [Text.Encoding]::...GetString / [System.Text.Encoding]::...GetString
	if strings.Contains(lower, "encoding]::") && strings.Contains(lower, "getstring") {
		return true
	}
	return false
}

func hasExecutor(lower string) bool {
	execTokens := map[string]bool{
		"iex":               true,
		"invoke-expression": true,
		"icm":               true,
		"invoke-command":    true,
		"saps":              true,
		"start-process":     true,
		"powershell":        true,
		"pwsh":              true,
		"cmd":               true,
		"cmd.exe":           true,
	}
	for _, tok := range splitLooseTokens(lower) {
		if execTokens[stripExePath(tok)] {
			return true
		}
	}
	// [scriptblock]::create(...) / .invoke(
	if strings.Contains(lower, "scriptblock]::create") {
		return true
	}
	// 调用运算符 `& $var` 或 `& (...)`
	for i := 0; i < len(lower); i++ {
		if lower[i] == '&' && (i == 0 || lower[i-1] != '&') && (i+1 < len(lower) && lower[i+1] != '&') {
			rest := strings.TrimLeft(lower[i+1:], " \t")
			if strings.HasPrefix(rest, "$") || strings.HasPrefix(rest, "(") {
				return true
			}
		}
	}
	return false
}

func containsIdentifierOrCall(lower, sub string) bool {
	idx := 0
	for {
		pos := strings.Index(lower[idx:], sub)
		if pos < 0 {
			return false
		}
		abs := idx + pos
		beforeOK := abs == 0 || !isIdentChar(rune(lower[abs-1]))
		afterIdx := abs + len(sub)
		afterOK := afterIdx == len(lower) || !isIdentChar(rune(lower[afterIdx]))
		if beforeOK && afterOK {
			return true
		}
		idx = abs + len(sub)
	}
}

func isIdentChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func splitLooseTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == ';' || r == '|' || r == '(' || r == ')' || r == '{' || r == '}'
	})
}
