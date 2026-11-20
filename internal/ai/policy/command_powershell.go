package policy

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/opskat/opskat/internal/pkg/pwshlex"
)

// PowerShellCommand 是一条 PowerShell 命令在策略面上的形状（用于 shell=powershell 的 SSH 资产）。
//
// 本地没有完整的 PowerShell 解析器，两种用途分开对待：
//   - Complete 时整串落在 completePowerShellCommands 认得的语法子集里，SubCommands 就是这条命令
//     会调用的全部命令，allow 规则与 grant 逐条判定；
//   - 否则 SubCommands 是尽力而为地从脚本块、子表达式、括号、双引号 $(…)、赋值右侧里找出来的
//     命令，只用来匹配 deny 规则和给出提示规则。这时哪怕少拆一个单元，剩下的全命中 allow 就会被
//     直接执行（`(Get-Item x).Delete()` 只拆得出 Get-Item），所以 allow 规则与 grant 不放行，
//     交给模型审核或人工。
type PowerShellCommand struct {
	SubCommands []string
	Complete    bool
}

// ParsePowerShellCommand 解析一条 PowerShell 命令。引号、注释、here-string、括号未闭合，
// 或出现 PowerShell 当作引号的弯引号时返回错误，由调用方按 DecideUnenumerableShell 判定。
func ParsePowerShellCommand(command string) (PowerShellCommand, error) {
	if err := pwshlex.CheckSyntax(command); err != nil {
		return PowerShellCommand{}, err
	}
	if units, ok := completePowerShellCommands(command); ok {
		return PowerShellCommand{SubCommands: units, Complete: true}, nil
	}
	var out []string
	extractPSBlock(normalizePSWhitespace(command), &out)
	return PowerShellCommand{SubCommands: out}, nil
}

// normalizePSWhitespace 把 PowerShell 视为空白的非 ASCII 字符（NBSP、U+0085、U+2028、全角空格……）
// 换成空格，单独的 CR 换成 LF（PowerShell 把它当换行），破折号 – — ― 换成 -（PowerShell 把它们
// 当参数前缀）。只用于拆出匹配用的子命令，不改变送审或执行的原文。
func normalizePSWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case r == '\u2013' || r == '\u2014' || r == '\u2015':
			return '-'
		case r >= utf8.RuneSelf && unicode.IsSpace(r):
			return ' '
		}
		return r
	}, s)
}

// psKeywords 是 PowerShell 的语言关键字：它们开头的语句不是命令调用。
var psKeywords = map[string]bool{
	"begin": true, "break": true, "catch": true, "class": true, "configuration": true, "continue": true,
	"data": true, "define": true, "do": true, "dynamicparam": true, "else": true, "elseif": true,
	"end": true, "enum": true, "exit": true, "filter": true, "finally": true, "for": true,
	"foreach": true, "from": true, "function": true, "hidden": true, "if": true, "in": true,
	"inlinescript": true, "parallel": true, "param": true, "process": true, "return": true,
	"sequence": true, "static": true, "switch": true, "throw": true, "trap": true, "try": true,
	"until": true, "using": true, "var": true, "while": true, "workflow": true,
}

// MatchPowerShellRule 按 PowerShell 的语义匹配 allow 规则与 grant：命令名与参数都不区分大小写；
// \ 与 / 同为路径分隔符——两边都换成 /，规则里的 \ 才不会被 glob 当成转义（否则
// `Get-ChildItem C:\logs` 连它自己都匹配不上），* 也不会跨目录（C:\logs\* 不匹配 C:\logs\..\..\Windows）；
// 内置别名按它指向的命令匹配（ls / dir / gci 命中 Get-ChildItem 的规则），但只认 5.1 与 7 里指向
// 同一个命令的别名：sc 在 5.1 是 Set-Content、在 7 里是 sc.exe。
func MatchPowerShellRule(rule, command string) bool {
	if matchPowerShellForm(rule, command) {
		return true
	}
	head, rest := splitPSHead(strings.TrimSpace(command))
	alias := strings.ToLower(head)
	if target, ok := powerShellBuiltinAliases[alias]; ok && !powerShellVersionSpecificAliases[alias] {
		return matchPowerShellForm(rule, joinPSHead(target, rest))
	}
	return false
}

// MatchPowerShellDenyRule 匹配 deny 规则：在 MatchPowerShellRule 之外，还按命令实际可能解析到的
// cmdlet 匹配——去掉调用运算符（& Remove-Item、. Remove-Item）、模块限定
// （Microsoft.PowerShell.Management\Remove-Item），并认任一版本的内置别名（rm / del / ri → Remove-Item）。
// deny 只能尽力而为：iex、& $name 这类动态调用本地看不出来，靠模型审核与人工兜底。
func MatchPowerShellDenyRule(rule, command string) bool {
	for _, c := range powerShellCommandForms(command) {
		if matchPowerShellForm(rule, c) {
			return true
		}
	}
	return false
}

func matchPowerShellForm(rule, command string) bool {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), `\`, "/") }
	return MatchCommandRule(norm(rule), norm(command))
}

// splitPSHead 把一条子命令切成命令名与其余部分；%{ … } / ?{ … } 的命令名是 % / ?。
func splitPSHead(cmd string) (head, rest string) {
	if len(cmd) > 1 && (cmd[0] == '%' || cmd[0] == '?') && cmd[1] == '{' {
		return cmd[:1], cmd[1:]
	}
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return cmd[:i], cmd[i+1:]
	}
	return cmd, ""
}

func joinPSHead(head, rest string) string { return strings.TrimSpace(head + " " + rest) }

// powerShellCommandForms 返回一条子命令的原样形式，以及按调用运算符、模块限定、内置别名
// 还原后的形式。
func powerShellCommandForms(command string) []string {
	forms := []string{command}
	cmd := strings.TrimSpace(command)
	if strings.HasPrefix(cmd, "&") || strings.HasPrefix(cmd, ". ") {
		head, rest := splitPSHead(strings.TrimSpace(cmd[1:]))
		if unq := strings.Trim(head, `'"`); !strings.ContainsAny(unq, `'"$`) {
			head = unq
		}
		cmd = joinPSHead(head, rest)
		forms = append(forms, cmd)
	}
	head, rest := splitPSHead(cmd)
	if i := strings.LastIndexByte(head, '\\'); i > 0 && head[0] != '.' && !strings.ContainsAny(head[:i], `:\/`) {
		head = head[i+1:]
		forms = append(forms, joinPSHead(head, rest))
	}
	if target, ok := powerShellBuiltinAliases[strings.ToLower(head)]; ok {
		forms = append(forms, joinPSHead(target, rest))
	}
	return forms
}

// --- 尽力而为的子命令提取（拆不全的命令的 deny 匹配与提示） ---

// extractPSBlock 按顶层语句分隔符切分并提取所有可执行子命令。
func extractPSBlock(s string, out *[]string) {
	for _, seg := range splitPSStatements(s) {
		extractPSStatement(seg, out)
	}
}

// splitPSStatements 在顶层按 ;、|、换行、&&、|| 切分语句，跳过注释、字符串与转义。
func splitPSStatements(s string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			parts = append(parts, t)
		}
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '<' && i+1 < len(s) && s[i+1] == '#' {
			if end, ok := pwshlex.BlockCommentEnd(s, i); ok {
				cur.WriteByte(' ')
				i = end
				continue
			}
		}
		if pwshlex.CommentStartsAt(s, i) {
			i = pwshlex.LineCommentEnd(s, i)
			continue
		}
		if next, err := pwshlex.SkipNonCode(s, i); err == nil && next != i {
			cur.WriteString(s[i : next+1])
			i = next
			continue
		}
		switch ch {
		case '(', '{', '[':
			depth++
			cur.WriteByte(ch)
		case ')', '}', ']':
			depth--
			cur.WriteByte(ch)
		case ';', '\n':
			if depth == 0 {
				flush()
			} else {
				cur.WriteByte(ch)
			}
		case '|':
			if depth == 0 {
				if i+1 < len(s) && s[i+1] == '|' {
					i++
				}
				flush()
			} else {
				cur.WriteByte(ch)
			}
		case '&':
			if depth == 0 && i+1 < len(s) && s[i+1] == '&' {
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

// extractPSStatement 解析单条语句：展开控制流、赋值右侧与嵌套的块和子表达式。
func extractPSStatement(stmt string, out *[]string) {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return
	}
	// 控制流关键字（if / foreach / try …）本身不是命令，只展开其中的条件与块。
	if psKeywords[strings.ToLower(firstPSWord(stmt))] {
		extractPSNested(stmt, out)
		return
	}
	// 变量赋值（$x = Get-CimInstance …）：提取右侧。
	if rhs, ok := splitPSAssignment(stmt); ok {
		extractPSStatement(rhs, out)
		return
	}
	if isPSExecutableHead(stmt) {
		*out = append(*out, stmt)
	}
	extractPSNested(stmt, out)
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

// splitPSAssignment 识别顶层 `$var = <rhs>` 赋值（排除 ==）。
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

// isPSExecutableHead 判断语句头是否是命令或 .NET 静态调用，排除字符串、数字、变量、括号表达式、
// @{…} / @(…) 与纯类型转换。
func isPSExecutableHead(stmt string) bool {
	switch stmt[0] {
	case '\'', '"', '(', '@', '$':
		return false
	case '[':
		// [System.IO.File]::Delete(...) 算命令，[pscustomobject]@{...} 不算
		return strings.Contains(stmt, "::")
	}
	return stmt[0] < '0' || stmt[0] > '9'
}

// extractPSNested 递归提取语句中脚本块 { … }、括号 ( … ) / $( … ) / @( … )、双引号内 $( … )
// 与哈希表值里的子命令。
func extractPSNested(s string, out *[]string) {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			if end, err := pwshlex.DoubleQuoteEnd(s, i); err == nil {
				extractPSDoubleQuoteSubExprs(s[i+1:end], out)
				i = end
				continue
			}
		case ch == '@' && i+1 < len(s) && s[i+1] == '{':
			if end, err := pwshlex.MatchingClose(s, i+1); err == nil {
				extractPSHashtableValues(s[i+2:end], out)
				i = end
				continue
			}
		case ch == '{' || ch == '(':
			if end, err := pwshlex.MatchingClose(s, i); err == nil {
				extractPSBlock(s[i+1:end], out)
				i = end
				continue
			}
		}
		if next, err := pwshlex.SkipNonCode(s, i); err == nil {
			i = next
		}
	}
}

func extractPSDoubleQuoteSubExprs(inner string, out *[]string) {
	for i := 0; i < len(inner); i++ {
		if inner[i] == '`' {
			i++
			continue
		}
		if inner[i] == '$' && i+1 < len(inner) && inner[i+1] == '(' {
			if end, err := pwshlex.MatchingClose(inner, i+1); err == nil {
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
