package sqldb

import (
	"errors"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// ScriptDialect 是方言里「SQL 文件怎么切成语句」：实现它的方言才能运行 SQL 文件，切分按它给出的
// 词法（ScriptSyntax）由 SplitScript 完成；没实现的，Session.RunScript 以 dbdriver.ErrUnsupported
// 拒绝。驱动描述的 dbdriver.CapRunSQLFile 与它一起声明。
type ScriptDialect interface {
	ScriptSyntax() ScriptSyntax
}

// ScriptSyntax 是方言脚本的词法：哪些记号里的结束符不结束语句。所有方言都认 -- 到行尾的注释与
// /* */ 注释；没有 BatchSeparator 的方言以分号（Delimiter 时是当前的分隔符）结束语句。
type ScriptSyntax struct {
	// Quotes 是方言的引号（字符串与标识符）：引号里的内容原样属于语句。
	Quotes []ScriptQuote
	// TriggerBodies 表示 CREATE [TEMP | TEMPORARY] TRIGGER 语句的触发器体里有以分号结束的语句：
	// 这种语句直到「; END ;」才结束（SQLite）。体里的 CASE ... END 前面不是分号，不会被当成结尾。
	TriggerBodies bool
	// AtomicBodies 表示 CREATE [OR REPLACE] FUNCTION | PROCEDURE 语句里（括号之外）的 BEGIN ... END
	// 是函数体，体里的分号不结束语句；体里的 CASE ... END 成对计入（PostgreSQL 的 BEGIN ATOMIC，
	// 与 psql 的认法一致）。
	AtomicBodies bool
	// HashComments 表示 # 到行尾也是注释（MySQL）。
	HashComments bool
	// SpacedDashComments 表示 -- 后面要跟空白或控制字符才是注释（MySQL），否则是两个减号。
	SpacedDashComments bool
	// ExecutableComments 表示 /*! ... */ 与 /*+ ... */ 不是注释，而是语句的一部分（MySQL 的版本注释
	// 与优化器提示：mysqldump 的文件里有只由它构成、要执行的语句）。
	ExecutableComments bool
	// NestedComments 表示 /* */ 注释可以嵌套（PostgreSQL、SQL Server）。
	NestedComments bool
	// Delimiter 表示认客户端的 DELIMITER 命令（MySQL）：语句开头的 DELIMITER xx 一行把之后语句的
	// 结束符换成 xx，这一行本身不是语句。当前的结束符经 ScriptChunk.State 带到下一次切分。
	Delimiter bool
	// DollarQuotes 表示认 $$ ... $$ 与 $tag$ ... $tag$ 引起的字符串（PostgreSQL）。
	DollarQuotes bool
	// EscapeStrings 表示 E'...'（紧挨着 E 的单引号字符串）里的反斜杠转义下一个字符（PostgreSQL）。
	EscapeStrings bool
	// BatchSeparator 不为空时，分号不结束语句：单独一行（前面可以有空白，后面可以跟执行次数与 --
	// 注释，不分大小写）的它结束一个批，整个批作为一条语句交给数据库执行；后面跟着次数 n 时这个批
	// 执行 n 次（SQL Server 的 GO）。
	BatchSeparator string
	// PsqlCommands 表示认出只有 psql 才能执行的写法：COPY ... FROM stdin 与反斜杠元命令。切分在那里
	// 停下（dbdriver.ScriptSplit.Unsupported），建议改用 psql（PostgreSQL）。
	PsqlCommands bool
}

// ScriptQuote 是一种引号：从 Open 到 Close；连写两个 Close 表示一个 Close 字符本身，不结束引号。
type ScriptQuote struct {
	Open, Close byte
	// Backslash 表示引号里的反斜杠转义下一个字符（MySQL 的字符串）。
	Backslash bool
}

// defaultDelimiter 是语句的结束符，Delimiter 方言在 DELIMITER 换掉它之前也用它。
const defaultDelimiter = ";"

// psqlTool 是 PostgreSQL 那些不支持的写法该改用的命令行工具。
const psqlTool = "psql"

// SplitScript 按 syntax 切分一段脚本，实现 dbdriver.ScriptSplitter 的约定：给出 chunk.Text 里完整的
// 语句（不含结束符，从语句的第一个记号算起，跳过空语句），用掉到最后一个结束符为止的文本；
// chunk.Final 时用掉全部文本，结尾没有结束符的语句也给出，只剩空白与注释的结尾丢弃。没写完的引号、
// 注释与语句留给下一次。State 是 Delimiter 方言当前的结束符（默认的分号时为空），别的方言原样带回。
func SplitScript(syntax ScriptSyntax, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	s := scriptScanner{syntax: syntax, text: chunk.Text, final: chunk.Final, lineStart: true}
	s.reset()
	if syntax.BatchSeparator == "" {
		s.delimiter = defaultDelimiter
		if syntax.Delimiter && chunk.State != "" {
			s.delimiter = chunk.State
		}
	}
	split := &dbdriver.ScriptSplit{Statements: []dbdriver.ScriptStatement{}, State: chunk.State}
	for s.pos < len(s.text) {
		switch s.next() {
		case scanMore:
			// 文本在一个记号中间结束：从上一个结束符之后再来。
			return split, nil
		case scanEnd:
			if statement, ok := s.statement(s.end); ok {
				for range s.repeat {
					split.Statements = append(split.Statements, statement)
				}
			}
			split.Consumed = s.pos
			s.reset()
		case scanDelimiter:
			split.Consumed = s.pos
			split.State = ""
			if s.delimiter != defaultDelimiter {
				split.State = s.delimiter
			}
			s.reset()
		case scanUnsupported:
			split.Unsupported = s.unsupported
			return split, nil
		case scanError:
			return nil, s.err
		}
	}
	if chunk.Final {
		if statement, ok := s.statement(len(s.text)); ok {
			split.Statements = append(split.Statements, statement)
		}
		split.Consumed = len(s.text)
	}
	return split, nil
}

// scanEvent 是扫过一个记号的结果。
type scanEvent int

const (
	// scanToken：一个不结束语句的记号（或空白、注释）。
	scanToken scanEvent = iota
	// scanMore：文本在这个记号中间结束，不是最后一段时要等后面的文本。
	scanMore
	// scanEnd：语句在 end 结束（结束符或分隔符行已扫过），执行 repeat 次。
	scanEnd
	// scanDelimiter：扫过一行 DELIMITER 命令，delimiter 已换成新的结束符。
	scanDelimiter
	// scanUnsupported：认出了不支持的写法（unsupported）。
	scanUnsupported
	// scanError：脚本写错了（err）。
	scanError
)

// scriptScanner 逐个记号扫描一段脚本。
type scriptScanner struct {
	syntax ScriptSyntax
	text   string
	final  bool
	pos    int
	// delimiter 是当前的语句结束符；有 BatchSeparator 的方言没有结束符（空）。
	delimiter string
	// lineStart 表示从这一行开头到当前位置只有空白（或刚扫过一个 -- 注释）。
	lineStart bool

	// start 是当前语句第一个记号的位置；-1 表示还没有记号。
	start int
	// words 是当前语句开头连续的词（大写），用来认出 CREATE TRIGGER / CREATE FUNCTION / COPY；一出现
	// 别的记号或够了 maxLeadingWords 个就不再记。
	words      []string
	leadingEnd bool
	// trigger 表示当前语句是带触发器体的 CREATE TRIGGER；afterSemi / afterEnd 是体里最近的记号是
	// 分号、是紧跟在分号后的 END。
	trigger   bool
	afterSemi bool
	afterEnd  bool
	// routine 表示当前语句是 CREATE [OR REPLACE] FUNCTION | PROCEDURE；depth 是它括号之外未闭合的
	// BEGIN（与体里的 CASE）层数，parens 是未闭合的括号数。
	routine bool
	depth   int
	parens  int
	// copy 表示当前语句是 COPY，afterFrom 是它最近的记号是 FROM。
	copy      bool
	afterFrom bool
	// lastWord / lastWordEnd 是最近一个词（大写）与它结束的位置，用来认出紧挨着的 E'...'。
	lastWord    string
	lastWordEnd int

	// end / repeat 是 scanEnd 时语句结束的位置与执行次数。
	end, repeat int
	unsupported *dbdriver.ScriptUnsupported
	err         error
}

// maxLeadingWords 是认语句种类要看的开头的词数：CREATE OR REPLACE FUNCTION。
const maxLeadingWords = 4

func (s *scriptScanner) reset() {
	s.start = -1
	s.words = s.words[:0]
	s.leadingEnd = false
	s.trigger, s.afterSemi, s.afterEnd = false, false, false
	s.routine, s.depth, s.parens = false, 0, 0
	s.copy, s.afterFrom = false, false
	s.lastWordEnd = -1
}

// statement 给出从当前语句第一个记号到 end 的语句；没有记号时没有语句。
func (s *scriptScanner) statement(end int) (dbdriver.ScriptStatement, bool) {
	if s.start < 0 {
		return dbdriver.ScriptStatement{}, false
	}
	return dbdriver.ScriptStatement{Text: strings.TrimRight(s.text[s.start:end], " \t\r\n"), Offset: s.start}, true
}

// more 是文本在当前记号中间结束时的结果：不是最后一段就等后面的文本，否则这个记号一直到文本结尾。
func (s *scriptScanner) more() scanEvent {
	if !s.final {
		return scanMore
	}
	s.pos = len(s.text)
	return scanToken
}

// next 扫过一个记号。
func (s *scriptScanner) next() scanEvent {
	c := s.text[s.pos]
	if isSpace(c) {
		s.pos++
		if c == '\n' {
			s.lineStart = true
		}
		return scanToken
	}
	if s.delimiter != "" {
		rest := s.text[s.pos:]
		if strings.HasPrefix(rest, s.delimiter) {
			return s.terminator()
		}
		if !s.final && len(rest) < len(s.delimiter) && strings.HasPrefix(s.delimiter, rest) {
			return scanMore
		}
	}
	switch {
	case c == '-' || c == '/' || c == '#':
		if event, ok := s.comment(c); ok {
			return event
		}
	case c == '$' && s.syntax.DollarQuotes:
		if event, ok := s.dollarQuoted(); ok {
			return event
		}
	case c == '\\' && s.syntax.PsqlCommands:
		return s.metaCommand()
	}
	if isWordByte(c) {
		return s.word()
	}
	for _, quote := range s.syntax.Quotes {
		if c == quote.Open {
			backslash := quote.Backslash ||
				(s.syntax.EscapeStrings && c == '\'' && s.lastWordEnd == s.pos && s.lastWord == "E")
			s.mark()
			s.other()
			return s.quoted(quote.Close, backslash)
		}
	}
	s.mark()
	s.other()
	switch c {
	case '(':
		s.parens++
	case ')':
		s.parens--
	}
	s.pos++
	return scanToken
}

// terminator 扫过当前位置的结束符：在触发器体或函数体里时它只是体里一条语句的结尾。
func (s *scriptScanner) terminator() scanEvent {
	start := s.pos
	s.pos += len(s.delimiter)
	s.lineStart = false
	if (s.trigger && !s.afterEnd) || s.depth > 0 {
		s.afterSemi, s.afterEnd = true, false
		s.afterFrom = false
		s.lastWordEnd = -1
		return scanToken
	}
	s.end, s.repeat = start, 1
	return scanEnd
}

// comment 扫过一个以 c 开头的注释；ok 为假表示 c 在这里不开始注释。
func (s *scriptScanner) comment(c byte) (scanEvent, bool) {
	if c == '#' {
		if !s.syntax.HashComments {
			return scanToken, false
		}
		return s.lineComment(1), true
	}
	if s.pos+1 >= len(s.text) {
		if !s.final {
			return scanMore, true
		}
		return scanToken, false
	}
	switch {
	case c == '-' && s.text[s.pos+1] == '-':
		if s.syntax.SpacedDashComments {
			if s.pos+2 >= len(s.text) && !s.final {
				return scanMore, true
			}
			if s.pos+2 < len(s.text) && s.text[s.pos+2] > ' ' {
				return scanToken, false
			}
		}
		return s.lineComment(2), true
	case c == '/' && s.text[s.pos+1] == '*':
		if s.syntax.ExecutableComments {
			if s.pos+2 >= len(s.text) && !s.final {
				return scanMore, true
			}
			if s.pos+2 < len(s.text) && (s.text[s.pos+2] == '!' || s.text[s.pos+2] == '+') {
				// 语句的一部分：分号、引号在里面都不算。
				s.mark()
				s.other()
				return s.skipTo("*/", 3), true
			}
		}
		s.lineStart = false
		if s.syntax.NestedComments {
			return s.nestedComment(), true
		}
		return s.skipTo("*/", 2), true
	}
	return scanToken, false
}

// lineComment 跳过从当前位置起 skip 个字节之后、到行尾（含换行）的注释。
func (s *scriptScanner) lineComment(skip int) scanEvent {
	event := s.skipTo("\n", skip)
	if event == scanToken {
		s.lineStart = true
	}
	return event
}

// skipTo 跳过从当前位置起 skip 个字节之后、到 terminator（含）为止的内容。
func (s *scriptScanner) skipTo(terminator string, skip int) scanEvent {
	i := strings.Index(s.text[s.pos+skip:], terminator)
	if i < 0 {
		return s.more()
	}
	s.pos += skip + i + len(terminator)
	return scanToken
}

// nestedComment 跳过一个可以嵌套的 /* */ 注释。
func (s *scriptScanner) nestedComment() scanEvent {
	depth := 0
	for i := s.pos; i+1 < len(s.text); {
		switch s.text[i : i+2] {
		case "/*":
			depth++
			i += 2
		case "*/":
			depth--
			i += 2
			if depth == 0 {
				s.pos = i
				return scanToken
			}
		default:
			i++
		}
	}
	return s.more()
}

// quoted 跳过一个引号里的内容（含两头的引号）；backslash 时反斜杠转义下一个字符。
func (s *scriptScanner) quoted(closing byte, backslash bool) scanEvent {
	for i := s.pos + 1; i < len(s.text); i++ {
		if backslash && s.text[i] == '\\' {
			i++
			continue
		}
		if s.text[i] != closing {
			continue
		}
		if i+1 < len(s.text) && s.text[i+1] == closing {
			i++
			continue
		}
		if i+1 == len(s.text) && !s.final {
			// 后面可能还有一个 closing，连起来是转义。
			return scanMore
		}
		s.pos = i + 1
		return scanToken
	}
	return s.more()
}

// dollarQuoted 扫过一个 $tag$ ... $tag$ 字符串；ok 为假表示这里的 $ 不开始它（如参数 $1）。
func (s *scriptScanner) dollarQuoted() (scanEvent, bool) {
	i := s.pos + 1
	if i < len(s.text) && isTagStart(s.text[i]) {
		for i < len(s.text) && isTagByte(s.text[i]) {
			i++
		}
	}
	if i >= len(s.text) {
		if !s.final {
			return scanMore, true
		}
		return scanToken, false
	}
	if s.text[i] != '$' {
		return scanToken, false
	}
	tag := s.text[s.pos : i+1]
	s.mark()
	s.other()
	return s.skipTo(tag, len(tag)), true
}

// metaCommand 是 psql 的反斜杠元命令：切分在它那里停下。
func (s *scriptScanner) metaCommand() scanEvent {
	end := s.pos + 1
	for end < len(s.text) && !isSpace(s.text[end]) && s.text[end] != '\\' {
		end++
	}
	if end == len(s.text) && !s.final {
		return scanMore
	}
	return s.unsupportedAt(s.pos, s.text[s.pos:end])
}

func (s *scriptScanner) unsupportedAt(offset int, construct string) scanEvent {
	s.unsupported = &dbdriver.ScriptUnsupported{Construct: construct, Tool: psqlTool, Offset: offset}
	return scanUnsupported
}

// mark 记下当前语句的第一个记号。
func (s *scriptScanner) mark() {
	if s.start < 0 {
		s.start = s.pos
	}
}

// other 记下一个既不是词也不是结束符的记号。
func (s *scriptScanner) other() {
	s.leadingEnd = true
	s.lineStart = false
	s.afterSemi, s.afterEnd = false, false
	s.afterFrom = false
	s.lastWordEnd = -1
}

// word 扫过一个词，并据此认出 DELIMITER 命令、批分隔符、语句的种类与体的边界。
func (s *scriptScanner) word() scanEvent {
	// 以词的字符开头的结束符（如 DELIMITER $$）紧跟在词后面时不属于这个词。
	delimiterInWord := s.delimiter != "" && isWordByte(s.delimiter[0])
	end := s.pos
	for end < len(s.text) && isWordByte(s.text[end]) {
		if delimiterInWord && end > s.pos && strings.HasPrefix(s.text[end:], s.delimiter) {
			break
		}
		end++
	}
	if end == len(s.text) && !s.final {
		return scanMore
	}
	word := strings.ToUpper(s.text[s.pos:end])
	if s.syntax.Delimiter && s.start < 0 && word == "DELIMITER" && end < len(s.text) && (s.text[end] == ' ' || s.text[end] == '\t') {
		return s.delimiterCommand(end)
	}
	if s.syntax.BatchSeparator != "" && s.lineStart && strings.EqualFold(word, s.syntax.BatchSeparator) {
		if event, ok := s.batchSeparator(end); ok {
			return event
		}
	}
	s.mark()
	s.pos = end
	s.lineStart = false
	if !s.leadingEnd {
		s.words = append(s.words, word)
		s.classify()
		if len(s.words) >= maxLeadingWords {
			s.leadingEnd = true
		}
	}
	if s.routine && s.parens == 0 {
		switch {
		case word == "BEGIN", word == "CASE" && s.depth > 0:
			s.depth++
		case word == "END" && s.depth > 0:
			s.depth--
		}
	}
	if s.copy {
		if s.afterFrom && word == "STDIN" {
			return s.unsupportedAt(s.start, "COPY ... FROM stdin")
		}
		s.afterFrom = word == "FROM"
	}
	s.afterEnd = s.trigger && s.afterSemi && word == "END"
	s.afterSemi = false
	s.lastWord, s.lastWordEnd = word, end
	return scanToken
}

// classify 按语句开头的词认出它的种类。
func (s *scriptScanner) classify() {
	if s.syntax.TriggerBodies && !s.trigger && isCreateTrigger(s.words) {
		s.trigger = true
		s.leadingEnd = true
	}
	if s.syntax.AtomicBodies && !s.routine {
		s.routine = isCreateRoutine(s.words)
	}
	if s.syntax.PsqlCommands && len(s.words) == 1 {
		s.copy = s.words[0] == "COPY"
	}
}

// delimiterCommand 扫过一行 DELIMITER 命令（词在 wordEnd 结束）：这一行的第一个词是新的结束符，
// 其后的内容忽略。
func (s *scriptScanner) delimiterCommand(wordEnd int) scanEvent {
	lineEnd := strings.IndexByte(s.text[wordEnd:], '\n')
	next := len(s.text)
	if lineEnd < 0 {
		if !s.final {
			return scanMore
		}
		lineEnd = len(s.text)
	} else {
		lineEnd += wordEnd
		next = lineEnd + 1
	}
	fields := strings.Fields(s.text[wordEnd:lineEnd])
	if len(fields) == 0 {
		s.err = errors.New("DELIMITER must be followed by a delimiter")
		return scanError
	}
	s.delimiter = fields[0]
	s.pos = next
	s.lineStart = true
	return scanDelimiter
}

// batchSeparator 认出一行批分隔符（分隔符这个词在 wordEnd 结束）：其后只能有空白、执行次数与 --
// 注释。ok 为假表示这一行不是分隔符，这个词照常属于语句。
func (s *scriptScanner) batchSeparator(wordEnd int) (scanEvent, bool) {
	lineEnd := strings.IndexByte(s.text[wordEnd:], '\n')
	next := len(s.text)
	if lineEnd < 0 {
		if !s.final {
			return scanMore, true
		}
		lineEnd = len(s.text)
	} else {
		lineEnd += wordEnd
		next = lineEnd + 1
	}
	rest := s.text[wordEnd:lineEnd]
	if i := strings.Index(rest, "--"); i >= 0 {
		rest = rest[:i]
	}
	repeat := 1
	if fields := strings.Fields(rest); len(fields) > 1 {
		return scanToken, false
	} else if len(fields) == 1 {
		n, err := strconv.Atoi(fields[0])
		if err != nil || n < 1 {
			return scanToken, false
		}
		repeat = n
	}
	s.end, s.repeat = s.pos, repeat
	s.pos = next
	s.lineStart = true
	return scanEnd, true
}

// isCreateTrigger 报告语句开头的词是不是 CREATE [TEMP | TEMPORARY] TRIGGER。
func isCreateTrigger(words []string) bool {
	if len(words) < 2 || words[0] != "CREATE" {
		return false
	}
	if words[1] == "TRIGGER" {
		return true
	}
	return len(words) == 3 && (words[1] == "TEMP" || words[1] == "TEMPORARY") && words[2] == "TRIGGER"
}

// isCreateRoutine 报告语句开头的词是不是 CREATE [OR REPLACE] FUNCTION | PROCEDURE。
func isCreateRoutine(words []string) bool {
	if len(words) < 2 || words[0] != "CREATE" {
		return false
	}
	kind := words[1]
	if kind == "OR" {
		if len(words) < 4 || words[2] != "REPLACE" {
			return false
		}
		kind = words[3]
	}
	return kind == "FUNCTION" || kind == "PROCEDURE"
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}

// isWordByte 报告 c 能不能出现在一个词里：字母、数字、下划线、$，以及多字节字符的字节。
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// isTagStart / isTagByte 报告 c 能不能开始 / 出现在 $tag$ 的 tag 里：同不加引号的标识符，但不含 $。
func isTagStart(c byte) bool {
	return c == '_' || c >= 0x80 || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isTagByte(c byte) bool {
	return isTagStart(c) || ('0' <= c && c <= '9')
}
