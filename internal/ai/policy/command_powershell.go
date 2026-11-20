package policy

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/opskat/opskat/internal/pkg/pwshlex"
)

// PowerShellCommand 是一条 PowerShell 命令在策略面上的形状（用于 shell=powershell 的 SSH 资产）。
//
// 本地没有 PowerShell 解析器，拆分只能近似，所以两种用途分开对待：
//   - SubCommands 用来匹配 deny 规则和给出提示规则，尽力而为：在 Plain 时它就是逐条语句，
//     否则是从脚本块、子表达式、括号、双引号 $(…)、赋值右侧里递归找出来的命令；
//   - 只有 Plain 时 allow 规则与 grant 才能放行。Plain 要求整串只有字面量命令与参数、
//     语句之间只用 ; | 换行 && || 分隔——没有变量、表达式、方法调用、脚本块、注释、重定向。
//     这样的串怎么切分没有歧义；其余写法哪怕少拆一个单元，剩下的单元全命中 allow 就会被
//     直接执行（`(Get-Item x).Delete()` 只拆得出 Get-Item），所以一律交给模型审核或人工。
type PowerShellCommand struct {
	SubCommands []string
	Plain       bool
}

// ParsePowerShellCommand 解析一条 PowerShell 命令。引号、注释、here-string、括号未闭合，
// 或出现 PowerShell 当作引号的弯引号时返回错误，由调用方按 DecideUnenumerableShell 判定。
func ParsePowerShellCommand(command string) (PowerShellCommand, error) {
	if err := pwshlex.CheckSyntax(command); err != nil {
		return PowerShellCommand{}, err
	}
	if units, ok := plainPowerShellStatements(command); ok {
		return PowerShellCommand{SubCommands: units, Plain: true}, nil
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

// plainArgByte 是 Plain 命令里允许出现在引号之外的 ASCII 标点（字母数字另算）。
// 不含 $ ` ( ) { } [ ] @ # < > ! ^ 与单个 &：它们会引入变量、表达式、脚本块、注释、重定向或调用运算符。
const plainArgBytes = `-_.:\/,=*?~%+`

// plainPowerShellStatements 判断整串是否是 Plain 命令；是则返回逐条语句。
func plainPowerShellStatements(s string) ([]string, bool) {
	var units []string
	segStart := 0
	needCommand := false // 刚经过 | && || 时下一段必须是命令
	cut := func(end int) bool {
		seg := strings.TrimSpace(s[segStart:end])
		if seg == "" {
			return !needCommand
		}
		if !plainCommandHead(seg) {
			return false
		}
		units = append(units, seg)
		needCommand = false
		return true
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\'':
			end, ok := pwshlex.SingleQuoteEnd(s, i)
			if !ok {
				return nil, false
			}
			i = end + 1
			continue
		case r == '"':
			end, ok := plainDoubleQuoteEnd(s, i)
			if !ok {
				return nil, false
			}
			i = end + 1
			continue
		case r == ';' || r == '\n' || r == '\r':
			if !cut(i) {
				return nil, false
			}
			segStart = i + 1
		case r == '|' || r == '&':
			if r == '&' && (i+1 >= len(s) || s[i+1] != '&') {
				return nil, false // 调用运算符或后台作业
			}
			if !cut(i) {
				return nil, false
			}
			if i+1 < len(s) && s[i+1] == s[i] {
				i++
			}
			needCommand = true
			segStart = i + 1
		case r == ' ' || r == '\t':
		case r < utf8.RuneSelf:
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(plainArgBytes, r) {
				return nil, false
			}
		default:
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
				return nil, false
			}
		}
		i += size
	}
	if !cut(len(s)) {
		return nil, false
	}
	return units, true
}

// plainDoubleQuoteEnd 找 Plain 命令里双引号字符串的结尾；里面出现 $ 或 `（会展开或转义）就不是 Plain。
func plainDoubleQuoteEnd(s string, start int) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '$', '`':
			return 0, false
		case '"':
			if i+1 < len(s) && s[i+1] == '"' {
				i++
				continue
			}
			return i, true
		}
	}
	return 0, false
}

// plainCommandHead 检查一条 Plain 语句的命令名：字母、_、. 或 \ 开头的裸词（数字、- 开头是表达式，
// 引号开头是字符串），不是语言关键字，且整条语句不含停止解析符 --%。
func plainCommandHead(stmt string) bool {
	head, _, _ := strings.Cut(stmt, " ")
	head, _, _ = strings.Cut(head, "\t")
	if strings.ContainsAny(head, `'"`) || psKeywords[strings.ToLower(head)] {
		return false
	}
	r, _ := utf8.DecodeRuneInString(head)
	if !unicode.IsLetter(r) && r != '_' && r != '.' && r != '\\' {
		return false
	}
	for _, f := range strings.Fields(stmt) {
		if f == "--%" {
			return false
		}
	}
	return true
}

// MatchPowerShellRule 按 PowerShell 的语义匹配一条命令规则：命令名与参数都不区分大小写；
// \ 与 / 同为路径分隔符——两边都换成 /，规则里的 \ 才不会被 glob 当成转义（否则
// `Get-ChildItem C:\logs` 连它自己都匹配不上），* 也不会跨目录（C:\logs\* 不匹配 C:\logs\..\..\Windows）。
// 用于 allow 规则与 grant。
func MatchPowerShellRule(rule, command string) bool {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), `\`, "/") }
	return MatchCommandRule(norm(rule), norm(command))
}

// MatchPowerShellDenyRule 在 MatchPowerShellRule 之外，还按命令实际解析到的 cmdlet 匹配：
// 去掉调用运算符（& Remove-Item、. Remove-Item）、模块限定（Microsoft.PowerShell.Management\Remove-Item），
// 并把内置别名换成它指向的 cmdlet（rm / del / ri → Remove-Item）。deny 只能尽力而为——
// iex、& $name 这类动态调用本地看不出来，靠模型审核与人工兜底。
func MatchPowerShellDenyRule(rule, command string) bool {
	for _, c := range powerShellCommandForms(command) {
		if MatchPowerShellRule(rule, c) {
			return true
		}
	}
	return false
}

// powerShellCommandForms 返回一条子命令的原样形式，以及按调用运算符、模块限定、内置别名
// 还原后的形式。
func powerShellCommandForms(command string) []string {
	forms := []string{command}
	cmd := strings.TrimSpace(command)
	if strings.HasPrefix(cmd, "&") || strings.HasPrefix(cmd, ". ") {
		head, rest, _ := strings.Cut(strings.TrimSpace(cmd[1:]), " ")
		if unq := strings.Trim(head, `'"`); !strings.ContainsAny(unq, `'"$`) {
			head = unq
		}
		cmd = strings.TrimSpace(head + " " + rest)
		forms = append(forms, cmd)
	}
	head, rest, _ := strings.Cut(cmd, " ")
	if i := strings.LastIndexByte(head, '\\'); i > 0 && head[0] != '.' && !strings.ContainsAny(head[:i], `:\/`) {
		head = head[i+1:]
		forms = append(forms, strings.TrimSpace(head+" "+rest))
	}
	if target, ok := powerShellBuiltinAliases[strings.ToLower(head)]; ok {
		forms = append(forms, strings.TrimSpace(target+" "+rest))
	}
	return forms
}

// --- 尽力而为的子命令提取（非 Plain 命令的 deny 匹配与提示） ---

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
