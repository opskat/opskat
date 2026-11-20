// Package pwshdecode 判断一条 PowerShell 命令是否把要执行的内容藏在编码/解码后面
// （如 -EncodedCommand / FromBase64String + Invoke-Expression），导致模型审核器看不到实际
// 要执行的代码。纯算法包，不依赖 permission 或扩展运行时，与 internal/pkg/shelldecode 对称。
package pwshdecode

import (
	"errors"
	"strings"
	"unicode"

	"github.com/opskat/opskat/internal/pkg/pwshlex"
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
	if err := pwshlex.CheckSyntax(command); err != nil {
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

// stripCommentsAndBackticks 去掉注释并展开反引号混淆（如 I`E`X → IEX）。字符串与 here-string
// 原样保留（双引号里的反引号也去掉："i`ex" 交给 & 调用就是 iex），其中的 # 不算注释。
func stripCommentsAndBackticks(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '<' && i+1 < len(s) && s[i+1] == '#':
			if end, ok := pwshlex.BlockCommentEnd(s, i); ok {
				b.WriteByte(' ')
				i = end
				continue
			}
		case pwshlex.CommentStartsAt(s, i):
			i = pwshlex.LineCommentEnd(s, i)
			b.WriteByte('\n')
			continue
		case ch == '@':
			if q, body, ok := pwshlex.HereStringAt(s, i); ok {
				if end, ok := pwshlex.HereStringEnd(s, body, q); ok {
					b.WriteString(s[i : end+1])
					i = end
					continue
				}
			}
		case ch == '\'':
			if end, ok := pwshlex.SingleQuoteEnd(s, i); ok {
				b.WriteString(s[i : end+1])
				i = end
				continue
			}
		case ch == '"':
			if end, err := pwshlex.DoubleQuoteEnd(s, i); err == nil {
				b.WriteString(strings.ReplaceAll(s[i:end+1], "`", ""))
				i = end
				continue
			}
		case ch == '`':
			if i+1 < len(s) && s[i+1] != '\n' && s[i+1] != '\r' {
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
