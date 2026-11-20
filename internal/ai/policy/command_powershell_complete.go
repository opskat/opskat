package policy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// completePowerShellCommands 用一个严格的 PowerShell 语法子集识别整串命令：识别得了，就保证
// 返回的子命令是这条命令会调用的全部命令（管道、&& / ||、脚本块、括号、$( … ) / @( … )、
// 双引号里的 $( … )、哈希表的值、if / foreach / try 里的都算），allow 规则与 grant 才能逐条判定；
// 子集之外的写法一律 ok=false。
//
// 子集只收"执行的代码都落在命令上"的结构，凡是能执行代码却没有命令名的都排除：
//   - 方法调用只认 psSafeMethods 里的纯函数（ToString、AddHours …），静态成员只认 psSafeStatics；
//   - 类型转换只认 psSafeCasts（[scriptblock]'…' 会把字符串变成代码）；
//   - ForEach-Object 只接脚本块（ForEach-Object Delete / -MemberName 是按名字调用方法）；
//   - 不收调用运算符 & 与点源、-as、注释、反引号续行、--%、写文件的重定向（只认 >$null 与 2>&1）、
//     函数 / 类定义，以及给 $env: 等限定变量和 $PS… 偏好变量赋值。
//
// 识别规则按 PowerShell 自己的解析器核对过（testdata/powershell_corpus.json，见
// TestPowerShellCompleteMatchesPowerShellParser）。
func completePowerShellCommands(s string) ([]string, bool) {
	p := &psParser{s: s}
	if !p.script(0) || p.i != len(s) {
		return nil, false
	}
	return p.units, true
}

type psParser struct {
	s     string
	i     int
	units []string
}

func (p *psParser) eof() bool { return p.i >= len(p.s) }

func (p *psParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}

func (p *psParser) peekAt(off int) byte {
	if p.i+off >= len(p.s) {
		return 0
	}
	return p.s[p.i+off]
}

func (p *psParser) skipSpaces() {
	for !p.eof() && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *psParser) skipSpacesAndNewlines() {
	for !p.eof() && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\n' || p.s[p.i] == '\r') {
		p.i++
	}
}

// word 返回从当前位置起由字母、数字、_、- 组成的词（不移动位置）。
func (p *psParser) word() string {
	j := p.i
	for j < len(p.s) && (isASCIIAlnum(p.s[j]) || p.s[j] == '_' || p.s[j] == '-') {
		j++
	}
	return p.s[p.i:j]
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// keywordAt 报告当前位置是否是关键字 kw（不区分大小写），且后面不再接词字符。
func (p *psParser) keywordAt(kw string) bool {
	w := p.word()
	return strings.EqualFold(w, kw)
}

// script 解析以 ; 或换行分隔的语句，直到 end（0 表示整串结束，否则是 ')' 或 '}'，不消费它）。
func (p *psParser) script(end byte) bool {
	for {
		for !p.eof() && strings.IndexByte(" \t;\n\r", p.s[p.i]) >= 0 {
			p.i++
		}
		if p.eof() {
			return end == 0
		}
		if c := p.peek(); c == end {
			return true
		} else if c == ')' || c == '}' {
			return false
		}
		if !p.statement() {
			return false
		}
		p.skipSpaces()
		if p.eof() {
			continue
		}
		switch p.peek() {
		case ';', '\n', '\r':
			p.i++
		case end:
			return true
		default:
			return false
		}
	}
}

func (p *psParser) statement() bool {
	switch w := strings.ToLower(p.word()); {
	case w == "if":
		return p.ifStatement()
	case w == "foreach":
		return p.foreachStatement()
	case w == "try":
		return p.tryStatement()
	case psKeywords[w]:
		return false
	}
	if p.peek() == '$' {
		save := p.i
		if ok, isAssign := p.assignmentHead(); isAssign {
			if !ok {
				return false
			}
			p.skipSpaces()
			return p.chain()
		}
		p.i = save
	}
	return p.chain()
}

// assignmentHead 识别 `$name =` / `$name +=` 这类赋值头。isAssign 为假时不是赋值（调用方回退）；
// 是赋值但目标不在允许范围（限定变量、$PS… 偏好变量、自动变量）时 ok 为假。
func (p *psParser) assignmentHead() (ok, isAssign bool) {
	p.i++ // $
	start := p.i
	for !p.eof() && (isASCIIAlnum(p.s[p.i]) || p.s[p.i] == '_' || p.s[p.i] == ':') {
		p.i++
	}
	name := p.s[start:p.i]
	p.skipSpaces()
	op := 0
	switch {
	case p.peek() == '=' && p.peekAt(1) != '=':
		op = 1
	case strings.IndexByte("+-*/", p.peek()) >= 0 && p.peekAt(1) == '=':
		op = 2
	default:
		return false, false
	}
	p.i += op
	return psAssignableVariable(name), true
}

// psAssignableVariable 是允许赋值的变量：普通变量名，不带作用域或驱动器限定（$env:、$function:），
// 不是 $PS… 偏好变量（$PSDefaultParameterValues 会改后续命令的参数），也不是自动变量。
func psAssignableVariable(name string) bool {
	if name == "" || strings.ContainsRune(name, ':') || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "ps") || psAutomaticVariables[lower] {
		return false
	}
	return true
}

var psAutomaticVariables = map[string]bool{
	"_": true, "args": true, "input": true, "this": true, "executioncontext": true, "host": true,
	"error": true, "myinvocation": true, "pwd": true, "home": true, "pid": true, "true": true,
	"false": true, "null": true, "shellid": true, "matches": true, "lastexitcode": true, "ofs": true,
	"foreach": true, "switch": true, "sender": true, "event": true, "eventargs": true,
	"eventsubscriber": true, "stacktrace": true, "nestedpromptlevel": true, "profile": true,
	"confirmpreference": true, "whatifpreference": true,
}

func (p *psParser) ifStatement() bool {
	p.i += len("if")
	if !p.parenCondition() || !p.blockAfterSpace() {
		return false
	}
	for {
		save := p.i
		p.skipSpacesAndNewlines()
		switch {
		case p.keywordAt("elseif"):
			p.i += len("elseif")
			if !p.parenCondition() || !p.blockAfterSpace() {
				return false
			}
		case p.keywordAt("else"):
			p.i += len("else")
			return p.blockAfterSpace()
		default:
			p.i = save
			return true
		}
	}
}

func (p *psParser) parenCondition() bool {
	p.skipSpaces()
	if p.peek() != '(' {
		return false
	}
	p.i++
	p.skipSpacesAndNewlines()
	if !p.chain() {
		return false
	}
	p.skipSpacesAndNewlines()
	if p.peek() != ')' {
		return false
	}
	p.i++
	return true
}

func (p *psParser) blockAfterSpace() bool {
	p.skipSpacesAndNewlines()
	return p.block()
}

// block 解析 { … }。
func (p *psParser) block() bool {
	if p.peek() != '{' {
		return false
	}
	p.i++
	if !p.script('}') {
		return false
	}
	p.i++
	return true
}

func (p *psParser) foreachStatement() bool {
	p.i += len("foreach")
	p.skipSpaces()
	if p.peek() != '(' {
		return false
	}
	p.i++
	p.skipSpaces()
	if p.peek() != '$' {
		return false
	}
	p.i++
	start := p.i
	for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_') {
		p.i++
	}
	if !psAssignableVariable(p.s[start:p.i]) {
		return false
	}
	p.skipSpaces()
	if !p.keywordAt("in") {
		return false
	}
	p.i += len("in")
	p.skipSpaces()
	if !p.chain() {
		return false
	}
	p.skipSpaces()
	if p.peek() != ')' {
		return false
	}
	p.i++
	return p.blockAfterSpace()
}

func (p *psParser) tryStatement() bool {
	p.i += len("try")
	if !p.blockAfterSpace() {
		return false
	}
	handlers := 0
	for {
		save := p.i
		p.skipSpacesAndNewlines()
		switch {
		case p.keywordAt("catch"):
			p.i += len("catch")
			p.skipSpaces()
			for p.peek() == '[' { // catch [System.IO.IOException], [System.Exception]
				if !p.typeName() {
					return false
				}
				p.skipSpaces()
				if p.peek() == ',' {
					p.i++
					p.skipSpaces()
				}
			}
			if !p.blockAfterSpace() {
				return false
			}
			handlers++
		case p.keywordAt("finally"):
			p.i += len("finally")
			return p.blockAfterSpace()
		default:
			p.i = save
			return handlers > 0
		}
	}
}

// chain 解析 pipeline { (&& | ||) pipeline }。
func (p *psParser) chain() bool {
	for {
		if !p.pipeline() {
			return false
		}
		p.skipSpaces()
		if (p.peek() == '&' && p.peekAt(1) == '&') || (p.peek() == '|' && p.peekAt(1) == '|') {
			p.i += 2
			p.skipSpacesAndNewlines()
			continue
		}
		return true
	}
}

// pipeline 解析 element { | element }。只有第一个元素可以是表达式，其后必须是命令。
func (p *psParser) pipeline() bool {
	if !p.element(true) {
		return false
	}
	for {
		p.skipSpaces()
		if p.peek() != '|' || p.peekAt(1) == '|' {
			return true
		}
		p.i++
		p.skipSpacesAndNewlines()
		if !p.element(false) {
			return false
		}
	}
}

func (p *psParser) element(first bool) bool {
	if p.atCommandStart() {
		return p.command()
	}
	if !first {
		return false
	}
	if !p.expression() {
		return false
	}
	return p.redirections()
}

// atCommandStart 报告当前位置是否开始一次命令调用：字母、_、\ 开头的裸词，.\ 或 ..\ 开头的路径，
// 以及 ForEach-Object / Where-Object 的别名 % 与 ?。
func (p *psParser) atCommandStart() bool {
	c := p.peek()
	switch {
	case c == '%' || c == '?':
		n := p.peekAt(1)
		return n == ' ' || n == '\t' || n == '{' || n == 0 || n == '\n' || n == '\r' || n == '|' || n == ';' || n == '}' || n == ')'
	case c == '.':
		return p.peekAt(1) == '\\' || p.peekAt(1) == '/' || (p.peekAt(1) == '.' && (p.peekAt(2) == '\\' || p.peekAt(2) == '/'))
	case c == '_' || c == '\\' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		return true
	case c >= utf8.RuneSelf:
		r, _ := utf8.DecodeRuneInString(p.s[p.i:])
		return unicode.IsLetter(r)
	}
	return false
}

// atElementEnd 报告当前位置是否结束一条命令或表达式。
func (p *psParser) atElementEnd() bool {
	if p.eof() {
		return true
	}
	switch c := p.peek(); c {
	case ';', '\n', '\r', '|', ')', '}':
		return true
	case '&':
		return p.peekAt(1) == '&'
	}
	return false
}

// psCommandHeadByte 是命令名里允许的 ASCII 字符。
func psCommandHeadByte(c byte) bool {
	return isASCIIAlnum(c) || strings.IndexByte(`-_.\/:`, c) >= 0
}

func (p *psParser) command() bool {
	start := p.i
	var head string
	if c := p.peek(); c == '%' || c == '?' {
		p.i++
		head = string(c)
	} else {
		for !p.eof() {
			c := p.peek()
			if c < utf8.RuneSelf {
				if !psCommandHeadByte(c) {
					break
				}
				p.i++
				continue
			}
			r, size := utf8.DecodeRuneInString(p.s[p.i:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
				return false
			}
			p.i += size
		}
		head = p.s[start:p.i]
		// 语句开头的 foreach 是关键字，已由 statement 分走；到这里的 foreach 是 ForEach-Object 的别名
		if lower := strings.ToLower(head); lower != "foreach" && psKeywords[lower] {
			return false
		}
	}
	// 命令名后只能是空白或命令结尾（% / ? 允许紧跟脚本块）
	if !p.atElementEnd() && p.peek() != ' ' && p.peek() != '\t' && (head != "%" && head != "?" || p.peek() != '{') {
		return false
	}
	forEach := isForEachObject(head)
	valueParams := 0 // ForEach-Object 后面还要几个参数值
	for {
		p.skipSpaces()
		if p.atElementEnd() {
			break
		}
		if p.atRedirection() {
			if !p.redirections() {
				return false
			}
			continue
		}
		argStart := p.i
		if !p.argument() {
			return false
		}
		if forEach {
			arg := p.s[argStart:p.i]
			switch {
			case valueParams > 0:
				valueParams--
			case strings.HasPrefix(arg, "{"):
			case strings.HasPrefix(arg, "-"):
				n, ok := psForEachParams[strings.ToLower(arg[1:])]
				if !ok {
					return false
				}
				valueParams = n
			default:
				return false
			}
		}
	}
	if valueParams > 0 {
		return false
	}
	p.units = append(p.units, strings.TrimSpace(p.s[start:p.i]))
	return true
}

// isForEachObject 报告命令名是否是 ForEach-Object（含别名与模块限定写法）。
func isForEachObject(head string) bool {
	h := strings.ToLower(head)
	if i := strings.LastIndexByte(h, '\\'); i >= 0 {
		h = h[i+1:]
	}
	return h == "foreach-object" || h == "%" || h == "foreach"
}

// psForEachParams 是 ForEach-Object 允许的参数（全名，不认缩写）及其后跟的值个数。-MemberName、
// -ArgumentList 按名字调用方法，不在其中。
var psForEachParams = map[string]int{
	"begin": 0, "process": 0, "end": 0, "remainingscripts": 0, "parallel": 0,
	"throttlelimit": 1, "timeoutseconds": 1, "asjob": 0, "usenewrunspace": 0,
}

// atRedirection 报告当前位置是否开始一次重定向（> / >> / 2> / *> …）。
func (p *psParser) atRedirection() bool {
	c := p.peek()
	return c == '>' || ((c >= '1' && c <= '6' || c == '*') && p.peekAt(1) == '>')
}

// redirections 解析零个或多个重定向，只认丢弃（>$null）与合并流（2>&1）。
func (p *psParser) redirections() bool {
	for {
		p.skipSpaces()
		if !p.atRedirection() {
			return true
		}
		if p.peek() != '>' {
			p.i++
		}
		p.i++ // >
		if p.peek() == '>' {
			p.i++
		}
		if p.peek() == '&' {
			if c := p.peekAt(1); c < '1' || c > '6' {
				return false
			}
			p.i += 2
			continue
		}
		p.skipSpaces()
		if !strings.EqualFold(p.s[p.i:min(p.i+5, len(p.s))], "$null") {
			return false
		}
		p.i += 5
		if !p.atElementEnd() && p.peek() != ' ' && p.peek() != '\t' {
			return false
		}
	}
}

// psArgBytes 是命令参数里允许出现在引号之外的 ASCII 标点（字母数字另算）。
const psArgBytes = `-_.:\/,=*?~%+!^`

// argument 解析一个命令参数：裸词、字符串、变量（带属性访问）、括号、子表达式、脚本块、哈希表、
// splatting 首尾相接组成的一个词。
func (p *psParser) argument() bool {
	start := p.i
	for !p.eof() {
		c := p.peek()
		switch {
		case c == ' ' || c == '\t' || p.atElementEnd():
			return p.i > start
		case c == '\'':
			if !p.singleQuoted() {
				return false
			}
		case c == '"':
			if !p.doubleQuoted() {
				return false
			}
		case c == '$':
			if !p.variableExpr() {
				return false
			}
		case c == '@':
			if !p.atExpr(true) {
				return false
			}
		case c == '(':
			if !p.parenExpr() {
				return false
			}
		case c == '{':
			if !p.block() {
				return false
			}
		case c == '-' && p.i+3 <= len(p.s) && p.s[p.i:p.i+3] == "--%":
			return false
		case c < utf8.RuneSelf:
			if !isASCIIAlnum(c) && strings.IndexByte(psArgBytes, c) < 0 {
				return false
			}
			p.i++
		default:
			r, size := utf8.DecodeRuneInString(p.s[p.i:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
				return false
			}
			p.i += size
		}
	}
	return p.i > start
}

func (p *psParser) singleQuoted() bool {
	for j := p.i + 1; j < len(p.s); j++ {
		if p.s[j] == '\'' {
			if j+1 < len(p.s) && p.s[j+1] == '\'' {
				j++
				continue
			}
			p.i = j + 1
			return true
		}
	}
	return false
}

// doubleQuoted 解析双引号字符串：$( … ) 子表达式按脚本解析（其中的命令计入子命令），$name 是取值。
func (p *psParser) doubleQuoted() bool {
	p.i++
	for !p.eof() {
		switch c := p.peek(); {
		case c == '`':
			p.i += 2
		case c == '"':
			if p.peekAt(1) == '"' {
				p.i += 2
				continue
			}
			p.i++
			return true
		case c == '$' && p.peekAt(1) == '(':
			p.i += 2
			if !p.script(')') {
				return false
			}
			p.i++
		case c == '$' && p.peekAt(1) == '{':
			return false
		default:
			p.i++
		}
	}
	return false
}

// variableExpr 解析 $name / $_ / $env:NAME / $( … ) 及其后的属性访问、白名单方法与下标。
func (p *psParser) variableExpr() bool {
	if p.peekAt(1) == '(' {
		p.i += 2
		if !p.script(')') {
			return false
		}
		p.i++
		return p.postfix()
	}
	p.i++
	start := p.i
	for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_' || p.peek() == ':') {
		p.i++
	}
	name := p.s[start:p.i]
	if name == "" || strings.HasSuffix(name, ":") || strings.Count(name, ":") > 1 {
		return false
	}
	if scope, _, ok := strings.Cut(name, ":"); ok && !strings.EqualFold(scope, "env") {
		return false
	}
	return p.postfix()
}

// atExpr 解析 @( … )、@{ … }，以及（参数里的）splatting @name。
func (p *psParser) atExpr(allowSplat bool) bool {
	switch n := p.peekAt(1); {
	case n == '(':
		p.i += 2
		if !p.script(')') {
			return false
		}
		p.i++
		return p.postfix()
	case n == '{':
		return p.hashtable()
	case allowSplat && (isASCIIAlnum(n) || n == '_'):
		p.i++
		for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_') {
			p.i++
		}
		return true
	}
	return false
}

func (p *psParser) parenExpr() bool {
	p.i++
	p.skipSpacesAndNewlines()
	if !p.chain() {
		return false
	}
	p.skipSpacesAndNewlines()
	if p.peek() != ')' {
		return false
	}
	p.i++
	return p.postfix()
}

// hashtable 解析 @{ key = value; … }，值按管道解析（其中的命令计入子命令）。
func (p *psParser) hashtable() bool {
	p.i += 2
	for {
		for !p.eof() && strings.IndexByte(" \t;\n\r", p.peek()) >= 0 {
			p.i++
		}
		if p.peek() == '}' {
			p.i++
			return true
		}
		switch p.peek() {
		case '\'':
			if !p.singleQuoted() {
				return false
			}
		case '"':
			if !p.doubleQuoted() {
				return false
			}
		default:
			start := p.i
			for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_' || p.peek() == '-') {
				p.i++
			}
			if p.i == start {
				return false
			}
		}
		p.skipSpaces()
		if p.peek() != '=' {
			return false
		}
		p.i++
		p.skipSpacesAndNewlines()
		if !p.chain() {
			return false
		}
	}
}

// postfix 解析属性访问 .name、白名单方法调用 .name( … ) 与下标 [ … ]。
func (p *psParser) postfix() bool {
	for {
		switch {
		case p.peek() == '.' && (isASCIIAlnum(p.peekAt(1)) || p.peekAt(1) == '_'):
			p.i++
			start := p.i
			for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_') {
				p.i++
			}
			if p.peek() == '(' {
				if !psSafeMethods[strings.ToLower(p.s[start:p.i])] || !p.callArgs() {
					return false
				}
			}
		case p.peek() == '[':
			p.i++
			p.skipSpaces()
			if !p.expression() {
				return false
			}
			p.skipSpaces()
			if p.peek() != ']' {
				return false
			}
			p.i++
		default:
			return true
		}
	}
}

// callArgs 解析方法调用的参数表 ( expr, … )。
func (p *psParser) callArgs() bool {
	p.i++ // (
	p.skipSpacesAndNewlines()
	if p.peek() == ')' {
		p.i++
		return true
	}
	for {
		if !p.unary() {
			return false
		}
		if !p.binaryTail(false) {
			return false
		}
		p.skipSpacesAndNewlines()
		switch p.peek() {
		case ',':
			p.i++
			p.skipSpacesAndNewlines()
		case ')':
			p.i++
			return true
		default:
			return false
		}
	}
}

// expression 解析表达式模式：操作数之间用比较、逻辑、算术、范围、格式化运算符与逗号连接。
func (p *psParser) expression() bool {
	if !p.unary() {
		return false
	}
	return p.binaryTail(true)
}

// binaryTail 解析操作数之后的 运算符 操作数 序列；commaList 为假时（方法参数里）逗号留给调用方。
func (p *psParser) binaryTail(commaList bool) bool {
	for {
		save := p.i
		p.skipSpaces()
		c := p.peek()
		switch {
		case c == ',' && commaList:
			p.i++
		case c == '.' && p.peekAt(1) == '.':
			p.i += 2
		case strings.IndexByte("+*/%", c) >= 0 && p.peekAt(1) != '=':
			p.i++
		case c == '-':
			p.i++
			if w := strings.ToLower(p.word()); w != "" && (w[0] < '0' || w[0] > '9') {
				if !psBinaryOperators[w] {
					return false
				}
				p.i += len(w)
			}
		default:
			p.i = save
			return true
		}
		p.skipSpacesAndNewlines()
		if !p.unary() {
			return false
		}
	}
}

// psBinaryOperators 是表达式里允许的 -运算符（-as 能把字符串转成脚本块，不在其中）。
var psBinaryOperators = func() map[string]bool {
	m := map[string]bool{"and": true, "or": true, "xor": true, "band": true, "bor": true, "bxor": true, "f": true}
	for _, op := range []string{"eq", "ne", "gt", "ge", "lt", "le", "like", "notlike", "match", "notmatch",
		"contains", "notcontains", "in", "notin", "replace", "split", "join"} {
		m[op], m["i"+op], m["c"+op] = true, true, true
	}
	return m
}()

// unary 解析一元运算符（-not、!、-、+）与其后的操作数。
func (p *psParser) unary() bool {
	p.skipSpaces()
	switch p.peek() {
	case '!', '+':
		p.i++
		return p.unary()
	case '-':
		if strings.EqualFold(p.s[p.i+1:min(p.i+4, len(p.s))], "not") && !isASCIIAlnum(p.peekAt(4)) {
			p.i += 4
			return p.unary()
		}
		p.i++
		return p.unary()
	}
	return p.primary()
}

func (p *psParser) primary() bool {
	switch c := p.peek(); {
	case c >= '0' && c <= '9':
		return p.number()
	case c == '\'':
		return p.singleQuoted() && p.postfix()
	case c == '"':
		return p.doubleQuoted() && p.postfix()
	case c == '$':
		return p.variableExpr()
	case c == '@':
		return p.atExpr(false)
	case c == '(':
		return p.parenExpr()
	case c == '{':
		return p.block()
	case c == '[':
		return p.typeExpr()
	}
	return false
}

// number 解析数字字面量：十进制（可带小数与 kb / mb / gb / tb / pb 后缀）或 0x 十六进制。
func (p *psParser) number() bool {
	if p.peek() == '0' && (p.peekAt(1) == 'x' || p.peekAt(1) == 'X') {
		p.i += 2
		start := p.i
		for !p.eof() && strings.IndexByte("0123456789abcdefABCDEF", p.peek()) >= 0 {
			p.i++
		}
		return p.i > start && !isASCIIAlnum(p.peek())
	}
	for !p.eof() && p.peek() >= '0' && p.peek() <= '9' {
		p.i++
	}
	if p.peek() == '.' && p.peekAt(1) >= '0' && p.peekAt(1) <= '9' {
		p.i++
		for !p.eof() && p.peek() >= '0' && p.peek() <= '9' {
			p.i++
		}
	}
	if w := strings.ToLower(p.word()); w != "" {
		switch w {
		case "kb", "mb", "gb", "tb", "pb":
			p.i += 2
		default:
			return false
		}
	}
	return !isASCIIAlnum(p.peek()) && p.peek() != '_'
}

// typeName 解析 [Type.Name]，返回前移动到 ] 之后。
func (p *psParser) typeName() bool {
	_, ok := p.readTypeName()
	return ok
}

func (p *psParser) readTypeName() (string, bool) {
	p.i++ // [
	start := p.i
	for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_' || p.peek() == '.') {
		p.i++
	}
	name := p.s[start:p.i]
	if name == "" || p.peek() != ']' {
		return "", false
	}
	p.i++
	return strings.TrimPrefix(strings.ToLower(name), "system."), true
}

// typeExpr 解析 [Type]::Member（白名单静态成员）或 [Type] 操作数（白名单类型转换）。
func (p *psParser) typeExpr() bool {
	name, ok := p.readTypeName()
	if !ok {
		return false
	}
	if p.peek() == ':' && p.peekAt(1) == ':' {
		p.i += 2
		start := p.i
		for !p.eof() && (isASCIIAlnum(p.peek()) || p.peek() == '_') {
			p.i++
		}
		member := strings.ToLower(p.s[start:p.i])
		allowed, known := psSafeStatics[name]
		if !known || member == "" {
			return false
		}
		if p.peek() == '(' {
			if !allowed.methods[member] && !allowed.anyMethod {
				return false
			}
			if !p.callArgs() {
				return false
			}
		} else if !allowed.props[member] && !allowed.anyProp {
			return false
		}
		return p.postfix()
	}
	if !psSafeCasts[name] {
		return false
	}
	p.skipSpaces()
	return p.unary()
}

// psSafeMethods 是可以在任意对象上调用的方法：只读、不执行代码的字符串 / 日期 / 数值方法。
var psSafeMethods = toSet(
	"tostring", "toupper", "tolower", "toupperinvariant", "tolowerinvariant", "trim", "trimstart", "trimend",
	"split", "substring", "replace", "contains", "startswith", "endswith", "indexof", "lastindexof",
	"padleft", "padright", "equals", "compareto",
	"addseconds", "addminutes", "addhours", "adddays", "addmonths", "addyears",
	"toshortdatestring", "tolongdatestring", "toshorttimestring", "tolongtimestring", "touniversaltime", "tolocaltime",
)

type psStaticMembers struct {
	anyMethod, anyProp bool
	methods, props     map[string]bool
}

// psSafeStatics 是允许访问的静态成员（类型名小写、去掉 system. 前缀）。[Environment] 只给属性：
// 它的方法里有 Exit、SetEnvironmentVariable。
var psSafeStatics = map[string]psStaticMembers{
	"math":        {anyMethod: true, anyProp: true},
	"datetime":    {props: toSet("now", "utcnow", "today"), methods: toSet("parse", "parseexact", "daysinmonth", "isleapyear")},
	"string":      {props: toSet("empty"), methods: toSet("join", "isnullorempty", "isnullorwhitespace", "format", "concat", "compare")},
	"timespan":    {methods: toSet("fromdays", "fromhours", "fromminutes", "fromseconds", "frommilliseconds")},
	"environment": {anyProp: true},
	"int":         {props: toSet("maxvalue", "minvalue"), methods: toSet("parse")},
	"int32":       {props: toSet("maxvalue", "minvalue"), methods: toSet("parse")},
	"int64":       {props: toSet("maxvalue", "minvalue"), methods: toSet("parse")},
	"long":        {props: toSet("maxvalue", "minvalue"), methods: toSet("parse")},
	"double":      {props: toSet("maxvalue", "minvalue"), methods: toSet("parse")},
}

// psSafeCasts 是允许的类型转换（[scriptblock] 等能把字符串变成代码的不在其中）。
var psSafeCasts = toSet(
	"pscustomobject", "ordered", "int", "int32", "int64", "long", "double", "decimal", "single", "float",
	"string", "bool", "boolean", "datetime", "timespan", "char", "byte", "version", "array", "hashtable", "void",
)

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
