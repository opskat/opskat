package sqldb

import (
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// ScriptDialect 是方言里「SQL 文件怎么切成语句」：实现它的方言才能运行 SQL 文件，切分按它给出的
// 词法（ScriptSyntax）由 SplitScript 完成；没实现的，Session.RunScript 以 dbdriver.ErrUnsupported
// 拒绝。驱动描述的 dbdriver.CapRunSQLFile 与它一起声明。
type ScriptDialect interface {
	ScriptSyntax() ScriptSyntax
}

// ScriptSyntax 是方言脚本的词法：哪些记号里的分号不结束语句。所有方言都认 -- 到行尾的注释、
// /* */ 注释，以及分号结束语句。
type ScriptSyntax struct {
	// Quotes 是方言的引号（字符串与标识符）：引号里的内容原样属于语句。
	Quotes []ScriptQuote
	// TriggerBodies 表示 CREATE [TEMP | TEMPORARY] TRIGGER 语句的触发器体里有以分号结束的语句：
	// 这种语句直到「; END ;」才结束（SQLite）。体里的 CASE ... END 前面不是分号，不会被当成结尾。
	TriggerBodies bool
}

// ScriptQuote 是一种引号：从 Open 到 Close；连写两个 Close 表示一个 Close 字符本身，不结束引号。
type ScriptQuote struct {
	Open, Close byte
}

// SplitScript 按 syntax 切分一段脚本，实现 dbdriver.ScriptSplitter 的约定：给出 chunk.Text 里完整的
// 语句（不含结束的分号，从语句的第一个记号算起，跳过空语句），用掉到最后一个结束符为止的文本；
// chunk.Final 时用掉全部文本，结尾没有分号的语句也给出，只剩空白与注释的结尾丢弃。没写完的引号、
// 注释与语句留给下一次。这里的词法没有跨语句的状态，State 原样带回。
func SplitScript(syntax ScriptSyntax, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	s := scriptScanner{syntax: syntax, text: chunk.Text, final: chunk.Final, start: -1}
	split := &dbdriver.ScriptSplit{Statements: []dbdriver.ScriptStatement{}, State: chunk.State}
	for s.pos < len(s.text) {
		end, ok := s.next()
		if !ok {
			// 文本在一个记号中间结束：从上一个结束符之后再来。
			return split, nil
		}
		if end {
			if statement, ok := s.statement(s.pos - 1); ok {
				split.Statements = append(split.Statements, statement)
			}
			split.Consumed = s.pos
			s.reset()
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

// scriptScanner 逐个记号扫描一段脚本。
type scriptScanner struct {
	syntax ScriptSyntax
	text   string
	final  bool
	pos    int

	// start 是当前语句第一个记号的位置；-1 表示还没有记号。
	start int
	// words 是当前语句开头连续的词（大写），用来认出 CREATE [TEMP] TRIGGER；一出现别的记号就不再记。
	words      []string
	leadingEnd bool
	// trigger 表示当前语句是带触发器体的 CREATE TRIGGER；afterSemi / afterEnd 是体里最近的记号是
	// 分号、是紧跟在分号后的 END。
	trigger   bool
	afterSemi bool
	afterEnd  bool
}

func (s *scriptScanner) reset() {
	s.start = -1
	s.words = s.words[:0]
	s.leadingEnd = false
	s.trigger = false
	s.afterSemi = false
	s.afterEnd = false
}

// statement 给出从当前语句第一个记号到 end 的语句；没有记号时没有语句。
func (s *scriptScanner) statement(end int) (dbdriver.ScriptStatement, bool) {
	if s.start < 0 {
		return dbdriver.ScriptStatement{}, false
	}
	return dbdriver.ScriptStatement{Text: strings.TrimRight(s.text[s.start:end], " \t\r\n"), Offset: s.start}, true
}

// next 扫过一个记号：end 表示它是结束语句的分号；ok 为假表示文本在这个记号中间结束（不是最后一段
// 时要等后面的文本）。
func (s *scriptScanner) next() (end, ok bool) {
	c := s.text[s.pos]
	switch {
	case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v':
		s.pos++
		return false, true
	case c == '-' || c == '/':
		if s.pos+1 >= len(s.text) && !s.final {
			return false, false
		}
		if c == '-' && s.peek(1) == '-' {
			return false, s.skipTo("\n", 2)
		}
		if c == '/' && s.peek(1) == '*' {
			return false, s.skipTo("*/", 2)
		}
	case c == ';':
		s.pos++
		if !s.trigger || s.afterEnd {
			return true, true
		}
		s.afterSemi, s.afterEnd = true, false
		return false, true
	case isWordByte(c):
		return false, s.word()
	}
	for _, quote := range s.syntax.Quotes {
		if c == quote.Open {
			s.mark()
			s.other()
			return false, s.quoted(quote.Close)
		}
	}
	s.mark()
	s.other()
	s.pos++
	return false, true
}

func (s *scriptScanner) peek(n int) byte {
	if s.pos+n < len(s.text) {
		return s.text[s.pos+n]
	}
	return 0
}

// mark 记下当前语句的第一个记号。
func (s *scriptScanner) mark() {
	if s.start < 0 {
		s.start = s.pos
	}
}

// other 记下一个既不是词也不是分号的记号。
func (s *scriptScanner) other() {
	s.leadingEnd = true
	s.afterSemi, s.afterEnd = false, false
}

// skipTo 跳过从当前位置起 skip 个字节之后、到 terminator（含）为止的注释。
func (s *scriptScanner) skipTo(terminator string, skip int) bool {
	i := strings.Index(s.text[s.pos+skip:], terminator)
	if i < 0 {
		if !s.final {
			return false
		}
		s.pos = len(s.text)
		return true
	}
	s.pos += skip + i + len(terminator)
	return true
}

// quoted 跳过一个引号里的内容（含两头的引号）。
func (s *scriptScanner) quoted(closing byte) bool {
	for i := s.pos + 1; i < len(s.text); i++ {
		if s.text[i] != closing {
			continue
		}
		if i+1 < len(s.text) && s.text[i+1] == closing {
			i++
			continue
		}
		if i+1 == len(s.text) && !s.final {
			// 后面可能还有一个 closing，连起来是转义。
			return false
		}
		s.pos = i + 1
		return true
	}
	if !s.final {
		return false
	}
	s.pos = len(s.text)
	return true
}

// word 扫过一个词，并据此认出 CREATE TRIGGER 与触发器体的结尾。
func (s *scriptScanner) word() bool {
	end := s.pos
	for end < len(s.text) && isWordByte(s.text[end]) {
		end++
	}
	if end == len(s.text) && !s.final {
		return false
	}
	s.mark()
	word := strings.ToUpper(s.text[s.pos:end])
	s.pos = end
	if s.syntax.TriggerBodies && !s.leadingEnd {
		s.words = append(s.words, word)
		s.trigger = isCreateTrigger(s.words)
		if len(s.words) >= 3 || s.trigger {
			s.leadingEnd = true
		}
	}
	s.afterEnd = s.trigger && s.afterSemi && word == "END"
	s.afterSemi = false
	return true
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

// isWordByte 报告 c 能不能出现在一个词里：字母、数字、下划线、$，以及多字节字符的字节。
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}
