package policy

import (
	"fmt"
	"strings"
	"unicode"
)

// ExtractPowerShellSubCommands 从 PowerShell 命令中提取所有可执行子命令。
//
// 用于标记为 shell=powershell 的 SSH 资产的策略规则匹配（allow / deny / grant）。
// 设计原则与 ExtractSubCommands（POSIX）一致：
//   - 未闭合的引号、注释块、括号或脚本块返回 parseErr，由调用方按 DecideUnenumerableShell 兜底（fail-closed）；
//   - 按顶层语句分隔符（;、|、换行、&&、||）拆分，并递归提取脚本块 { ... }、子表达式 $(...)、
//     括号表达式 (...) 以及双引号内 $(...) 中的子命令，确保 deny 规则能覆盖嵌套调用；
//   - 纯字符串字面量（如 '--- HEADER ---'）、纯字面量赋值（如 $p='C:\tmp'）和注释不算可执行命令；
//     右侧带命令的赋值（如 $o=Get-CimInstance ...）会提取右侧命令；
//   - .NET 静态方法调用（如 [System.IO.File]::Delete(...)）保留为子命令，防止通过 .NET API 绕过规则。
func ExtractPowerShellSubCommands(command string) ([]string, error) {
	if err := validatePowerShellSyntax(command); err != nil {
		return nil, err
	}
	var out []string
	extractPSBlock(command, &out)
	return out, nil
}

// validatePowerShellSyntax 检查引号、块注释与括号/花括号的闭合配对。
func validatePowerShellSyntax(s string) error {
	var stack []byte
	n := len(s)
	for i := 0; i < n; i++ {
		ch := s[i]
		// 块注释 <# ... #>
		if ch == '<' && i+1 < n && s[i+1] == '#' {
			end := strings.Index(s[i+2:], "#>")
			if end < 0 {
				return fmt.Errorf("unclosed PowerShell block comment <#")
			}
			i = i + 2 + end + 1
			continue
		}
		// 行注释 # ... \n
		if ch == '#' {
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		// Here-string @' ... '@ 或 @" ... "@
		if ch == '@' && i+1 < n && (s[i+1] == '\'' || s[i+1] == '"') {
			q := s[i+1]
			// Here-string 开头后面必须是行尾（允许空格/回车）
			j := i + 2
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			if j < n && s[j] == '\n' {
				endIdx, ok := findHereStringEnd(s, j+1, q)
				if !ok {
					return fmt.Errorf("unclosed PowerShell here-string @%c", q)
				}
				i = endIdx
				continue
			}
		}
		// 单引号字符串 '...'（'' 转义）
		if ch == '\'' {
			end, ok := scanPSSingleQuote(s, i)
			if !ok {
				return fmt.Errorf("unclosed single quote")
			}
			i = end
			continue
		}
		// 双引号字符串 "..."（` 转义，内部 $(...) 递归检查）
		if ch == '"' {
			end, err := scanPSDoubleQuote(s, i)
			if err != nil {
				return err
			}
			i = end
			continue
		}
		// 反引号转义下一个字符
		if ch == '`' {
			i++
			continue
		}
		switch ch {
		case '(', '{', '[':
			stack = append(stack, ch)
		case ')', '}', ']':
			if len(stack) == 0 || !matchingPSDelim(stack[len(stack)-1], ch) {
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

func matchingPSDelim(openCh, closeCh byte) bool {
	return (openCh == '(' && closeCh == ')') ||
		(openCh == '{' && closeCh == '}') ||
		(openCh == '[' && closeCh == ']')
}

func findHereStringEnd(s string, from int, quote byte) (int, bool) {
	n := len(s)
	for i := from; i < n; {
		if i+1 < n && s[i] == quote && s[i+1] == '@' {
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

func scanPSSingleQuote(s string, start int) (int, bool) {
	n := len(s)
	for i := start + 1; i < n; i++ {
		if s[i] == '\'' {
			if i+1 < n && s[i+1] == '\'' {
				i++
				continue
			}
			return i, true
		}
	}
	return 0, false
}

func scanPSDoubleQuote(s string, start int) (int, error) {
	n := len(s)
	for i := start + 1; i < n; i++ {
		switch s[i] {
		case '`':
			i++
		case '"':
			return i, nil
		case '$':
			if i+1 < n && s[i+1] == '(' {
				end, err := findMatchingParen(s, i+1)
				if err != nil {
					return 0, err
				}
				if err := validatePowerShellSyntax(s[i+2 : end]); err != nil {
					return 0, err
				}
				i = end
			}
		}
	}
	return 0, fmt.Errorf("unclosed double quote")
}

func findMatchingParen(s string, openIdx int) (int, error) {
	open := s[openIdx]
	var closeCh byte
	switch open {
	case '(':
		closeCh = ')'
	case '{':
		closeCh = '}'
	case '[':
		closeCh = ']'
	default:
		return 0, fmt.Errorf("invalid delimiter %q", open)
	}
	depth := 1
	n := len(s)
	for i := openIdx + 1; i < n; i++ {
		ch := s[i]
		if ch == '<' && i+1 < n && s[i+1] == '#' {
			end := strings.Index(s[i+2:], "#>")
			if end < 0 {
				return 0, fmt.Errorf("unclosed PowerShell block comment <#")
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
		if ch == '\'' {
			end, ok := scanPSSingleQuote(s, i)
			if !ok {
				return 0, fmt.Errorf("unclosed single quote")
			}
			i = end
			continue
		}
		if ch == '"' {
			end, err := scanPSDoubleQuote(s, i)
			if err != nil {
				return 0, err
			}
			i = end
			continue
		}
		if ch == '`' {
			i++
			continue
		}
		switch ch {
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unclosed %q", open)
}

// extractPSBlock 按顶层语句分隔符切分并提取所有可执行子命令。
func extractPSBlock(s string, out *[]string) {
	for _, seg := range splitPSStatements(s) {
		extractPSStatement(seg, out)
	}
}

// splitPSStatements 在顶层按 ;、|、\n、&&、|| 切分 PowerShell 语句，跳过注释与引号内部。
func splitPSStatements(s string) []string {
	var parts []string
	var cur strings.Builder
	depthParen, depthBrace, depthBracket := 0, 0, 0
	n := len(s)
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			parts = append(parts, t)
		}
		cur.Reset()
	}
	for i := 0; i < n; i++ {
		ch := s[i]
		// 块注释 <# ... #>
		if ch == '<' && i+1 < n && s[i+1] == '#' {
			end := strings.Index(s[i+2:], "#>")
			if end >= 0 {
				i = i + 2 + end + 1
				cur.WriteByte(' ')
				continue
			}
		}
		// 行注释 # ... \n
		if ch == '#' {
			for i < n && s[i] != '\n' {
				i++
			}
			if depthParen == 0 && depthBrace == 0 && depthBracket == 0 {
				flush()
			} else {
				cur.WriteByte('\n')
			}
			continue
		}
		// Here-string
		if ch == '@' && i+1 < n && (s[i+1] == '\'' || s[i+1] == '"') {
			q := s[i+1]
			j := i + 2
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			if j < n && s[j] == '\n' {
				if endIdx, ok := findHereStringEnd(s, j+1, q); ok {
					cur.WriteString(s[i : endIdx+1])
					i = endIdx
					continue
				}
			}
		}
		if ch == '\'' {
			if end, ok := scanPSSingleQuote(s, i); ok {
				cur.WriteString(s[i : end+1])
				i = end
				continue
			}
		}
		if ch == '"' {
			if end, err := scanPSDoubleQuote(s, i); err == nil {
				cur.WriteString(s[i : end+1])
				i = end
				continue
			}
		}
		if ch == '`' && i+1 < n {
			cur.WriteByte(ch)
			i++
			cur.WriteByte(s[i])
			continue
		}
		switch ch {
		case '(':
			depthParen++
			cur.WriteByte(ch)
		case ')':
			depthParen--
			cur.WriteByte(ch)
		case '{':
			depthBrace++
			cur.WriteByte(ch)
		case '}':
			depthBrace--
			cur.WriteByte(ch)
		case '[':
			depthBracket++
			cur.WriteByte(ch)
		case ']':
			depthBracket--
			cur.WriteByte(ch)
		case ';', '\n':
			if depthParen == 0 && depthBrace == 0 && depthBracket == 0 {
				flush()
			} else {
				cur.WriteByte(ch)
			}
		case '|':
			if depthParen == 0 && depthBrace == 0 && depthBracket == 0 {
				if i+1 < n && s[i+1] == '|' {
					i++
				}
				flush()
			} else {
				cur.WriteByte(ch)
			}
		case '&':
			if depthParen == 0 && depthBrace == 0 && depthBracket == 0 && i+1 < n && s[i+1] == '&' {
				i++
				flush()
			} else {
				cur.WriteByte(ch)
			}
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return parts
}

// extractPSStatement 解析单条 PowerShell 语句：处理控制流、变量赋值、嵌套块与子表达式。
func extractPSStatement(stmt string, out *[]string) {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return
	}

	// 控制流关键字（if / elseif / else / for / foreach / while / do / try / catch / finally / switch）：
	// 展开其条件括号和花括号内的子命令，关键字本身不记成命令。
	if handled := extractPSControlFlow(stmt, out); handled {
		return
	}

	// 变量赋值（如 $x = Get-CimInstance ... 或 $s = (Get-ChildItem ...).Sum）：
	// 提取右侧命令；纯字面量赋值不产生命令。
	if rhs, ok := splitPSAssignment(stmt); ok {
		extractPSStatement(rhs, out)
		return
	}

	// 提取语句中所有嵌套的脚本块 { ... }、子表达式 $(...)、括号表达式 (...) 与双引号内 $(...)。
	// 若语句头本身是可执行命令（而非纯字符串/变量/哈希表字面量），先记下主命令再递归嵌套内容。
	if isPSExecutableHead(stmt) {
		*out = append(*out, normalizePSSubCommand(stmt))
	}
	extractPSNested(stmt, out)
}

var psControlKeywords = map[string]bool{
	"if": true, "elseif": true, "else": true,
	"for": true, "foreach": true, "while": true, "do": true,
	"try": true, "catch": true, "finally": true, "switch": true,
	"function": true, "filter": true,
}

func extractPSControlFlow(stmt string, out *[]string) bool {
	head := firstPSWord(stmt)
	if !psControlKeywords[strings.ToLower(head)] {
		return false
	}
	extractPSNested(stmt, out)
	return true
}

func firstPSWord(s string) string {
	i := 0
	for i < len(s) {
		r := rune(s[i])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			i++
		} else {
			break
		}
	}
	return s[:i]
}

// splitPSAssignment 识别顶层 `$var = <rhs>` 赋值（排除 ==、!=、-eq 等比较）。
func splitPSAssignment(stmt string) (string, bool) {
	if len(stmt) < 3 || stmt[0] != '$' {
		return "", false
	}
	i := 1
	for i < len(stmt) && (unicode.IsLetter(rune(stmt[i])) || unicode.IsDigit(rune(stmt[i])) || stmt[i] == '_' || stmt[i] == ':') {
		i++
	}
	if i == 1 {
		return "", false
	}
	for i < len(stmt) && (stmt[i] == ' ' || stmt[i] == '\t') {
		i++
	}
	if i < len(stmt) && stmt[i] == '=' && (i+1 >= len(stmt) || stmt[i+1] != '=') {
		return strings.TrimSpace(stmt[i+1:]), true
	}
	return "", false
}

// isPSExecutableHead 判断一条 PowerShell 语句头部是否是可执行命令或方法调用，
// 排除纯字符串字面量、纯数字、纯变量访问、纯括号表达式和哈希表字面量 @{...}。
func isPSExecutableHead(stmt string) bool {
	if stmt == "" {
		return false
	}
	switch stmt[0] {
	case '\'', '"', '(', '@', '$':
		// 以单/双引号、括号、@{...}/@(...)、$var 开头的表达式本身不是命令名
		// （其内部的 $(...) / (...) / {...} 会由 extractPSNested 递归提取）
		return false
	case '[':
		// 类型静态调用如 [System.IO.File]::Delete(...) 或 [math]::Round(...) 算可执行表达式，
		// 纯类型转换如 [pscustomobject]@{...} 不算命令
		return strings.Contains(stmt, "::")
	}
	if stmt[0] >= '0' && stmt[0] <= '9' {
		return false
	}
	return true
}

// normalizePSSubCommand 压缩命令里的多余空白，便于与 Allow/Deny 规则匹配。
func normalizePSSubCommand(stmt string) string {
	return strings.TrimSpace(stmt)
}

// extractPSNested 扫描一条 PowerShell 语句中的脚本块 { ... }、括号表达式 (...)、
// 子表达式 $(...) 及双引号内部的 $(...)，递归提取其中的可执行子命令。
func extractPSNested(s string, out *[]string) {
	n := len(s)
	for i := 0; i < n; i++ {
		ch := s[i]
		if ch == '<' && i+1 < n && s[i+1] == '#' {
			if end := strings.Index(s[i+2:], "#>"); end >= 0 {
				i = i + 2 + end + 1
				continue
			}
		}
		if ch == '#' {
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '\'' {
			if end, ok := scanPSSingleQuote(s, i); ok {
				i = end
				continue
			}
		}
		if ch == '"' {
			if end, err := scanPSDoubleQuote(s, i); err == nil {
				extractPSDoubleQuoteSubExprs(s[i+1:end], out)
				i = end
				continue
			}
		}
		if ch == '`' {
			i++
			continue
		}
		// 哈希表字面量 @{ ... }：递归其值中的子表达式，但不把键名当成命令
		if ch == '@' && i+1 < n && s[i+1] == '{' {
			if end, err := findMatchingParen(s, i+1); err == nil {
				extractPSHashtableValues(s[i+2:end], out)
				i = end
				continue
			}
		}
		if ch == '{' || ch == '(' {
			if end, err := findMatchingParen(s, i); err == nil {
				extractPSBlock(s[i+1:end], out)
				i = end
				continue
			}
		}
	}
}

func extractPSDoubleQuoteSubExprs(inner string, out *[]string) {
	n := len(inner)
	for i := 0; i < n; i++ {
		if inner[i] == '`' {
			i++
			continue
		}
		if inner[i] == '$' && i+1 < n && inner[i+1] == '(' {
			if end, err := findMatchingParen(inner, i+1); err == nil {
				extractPSBlock(inner[i+2:end], out)
				i = end
			}
		}
	}
}

func extractPSHashtableValues(inner string, out *[]string) {
	for _, entry := range splitPSStatements(inner) {
		if _, val, ok := strings.Cut(entry, "="); ok {
			extractPSStatement(strings.TrimSpace(val), out)
		}
	}
}
