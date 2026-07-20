// Package cmdline 提供引号感知的命令行切词与 flag 解析，供统一 exec 下各资产类型的
// 命令 DSL 共用（mongo / etcd / kafka），以及 k8s 的 kubectl 参数解析。
//
// 只做词法层，不认识任何具体协议的动词：谁是 verb、哪些 flag 合法，由各类型的
// <name>_command.go 自己判断。
package cmdline

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Words 把命令串切成词，保留引号内的空格，并剥掉引号本身；显式的空词（单引号或
// 双引号包裹的空串，例如 `""`）会原样保留为一个空字符串元素，不做静默丢弃。
//
// 拒绝一切 shell 控制结构（管道、重定向、变量赋值、多语句、后台运行 `&`、取反 `!`）：
// 这些串最终会被送去执行 typed service 调用，不经过 shell，语义上没有"管道"这回事；
// 容忍它们只会让模型写出看似成功、实则参数被吃掉（或在真 shell 里被后台/取反）的命令。
func Words(s string) ([]string, error) {
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(s), "")
	if err != nil {
		return nil, fmt.Errorf("invalid command: %w", err)
	}
	if len(file.Stmts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	if len(file.Stmts) != 1 {
		return nil, fmt.Errorf("only a single command is supported")
	}

	stmt := file.Stmts[0]
	if len(stmt.Redirs) > 0 {
		return nil, fmt.Errorf("shell redirection is not supported")
	}
	if stmt.Background {
		return nil, fmt.Errorf("shell backgrounding (&) is not supported")
	}
	if stmt.Negated {
		return nil, fmt.Errorf("shell negation (!) is not supported")
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return nil, fmt.Errorf("only a simple command is supported")
	}
	if len(call.Assigns) > 0 {
		return nil, fmt.Errorf("shell variable assignments are not supported")
	}

	words := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		lit, err := wordLiteral(word)
		if err != nil {
			return nil, err
		}
		words = append(words, lit)
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return words, nil
}

func wordLiteral(word *syntax.Word) (string, error) {
	var b strings.Builder
	for _, part := range word.Parts {
		if err := appendWordPart(&b, part, false); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// appendWordPart writes part's literal value to b. inDblQuoted tracks whether
// part is nested inside a DblQuoted: mvdan's syntax package is a pure parser —
// it never resolves backslash escapes, even inside double quotes — so a Lit
// found there still carries the raw backslash-escaped source text (escaped
// quote, dollar, backtick, backslash) and must be unescaped here to get the
// value the shell would actually see. Outside double quotes those characters
// aren't special in the strings this package accepts (QuoteIfNeeded always
// quotes a value containing them), so no unescaping happens there.
func appendWordPart(b *strings.Builder, part syntax.WordPart, inDblQuoted bool) error {
	switch x := part.(type) {
	case *syntax.Lit:
		if inDblQuoted {
			b.WriteString(unescapeDblQuotedLit(x.Value))
		} else {
			b.WriteString(x.Value)
		}
		return nil
	case *syntax.SglQuoted:
		b.WriteString(x.Value)
		return nil
	case *syntax.DblQuoted:
		for _, inner := range x.Parts {
			if err := appendWordPart(b, inner, true); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported shell construct in command")
	}
}

// unescapeDblQuotedLit resolves the backslash escapes that are meaningful
// inside double quotes per POSIX: an escaped backslash, double quote,
// dollar sign, backtick, or a trailing line continuation. Any other
// backslash is left as-is — it has no special meaning there, so removing
// it would corrupt the literal value.
func unescapeDblQuotedLit(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\', '"', '$', '`':
				b.WriteByte(s[i+1])
				i++
				continue
			case '\n':
				i++
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Command 是切词后的结构：第一个词是 Verb，其余非 flag 词按序进 Args，
// `--k=v` 与裸 `--k`（值为 "true"）进 Flags。
type Command struct {
	Verb  string
	Args  []string
	Flags map[string]string
}

// Parse 解析富命令串。
func Parse(s string) (*Command, error) {
	words, err := Words(s)
	if err != nil {
		return nil, err
	}

	c := &Command{Verb: words[0], Flags: map[string]string{}}
	for _, w := range words[1:] {
		if !strings.HasPrefix(w, "--") {
			c.Args = append(c.Args, w)
			continue
		}
		name, value, found := strings.Cut(strings.TrimPrefix(w, "--"), "=")
		if name == "" {
			return nil, fmt.Errorf("malformed flag: %s", w)
		}
		if !found {
			value = "true"
		}
		if _, dup := c.Flags[name]; dup {
			return nil, fmt.Errorf("duplicate flag: --%s", name)
		}
		c.Flags[name] = value
	}
	return c, nil
}

// Render 把 Command 还原为命令串。Flags 按名称排序输出——审批弹窗与审计日志
// 需要同一个 Command 每次都渲染出完全相同的字符串，Go map 迭代顺序本身是随机的，
// 不排序就没法保证这一点。
func (c *Command) Render() string {
	parts := make([]string, 0, 1+len(c.Args)+len(c.Flags))
	parts = append(parts, c.Verb)
	for _, a := range c.Args {
		parts = append(parts, QuoteIfNeeded(a))
	}
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		if c.Flags[name] == "true" {
			parts = append(parts, "--"+name)
			continue
		}
		parts = append(parts, "--"+name+"="+QuoteIfNeeded(c.Flags[name]))
	}
	return strings.Join(parts, " ")
}

// safeUnquotedWord 匹配可以不加引号原样输出、且被 Words 解析回同一个词的字符集：
// 字母数字与一组在 shell 里没有特殊含义的标点。任何不满足这个集合的字符——无论是否
// 在当前已知的元字符清单里——都会被引号包裹，这是"允许表"而非"排除表"的安全性所在。
var safeUnquotedWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// QuoteIfNeeded 决定一个值要不要加引号，以及加哪种引号，使其与 Words 的剥引号逻辑
// 互逆（两者必须一起改）：只有匹配 safeUnquotedWord 的值才原样输出；其余一律加引号
// ——默认单引号，值本身含单引号时退化为转义后的双引号包裹。这是 shlex.quote 的标准
// 做法：用"哪些字符绝对安全"的允许表，而不是"哪些字符已知有害"的排除表，
// 后者漏一个 shell 元字符（`#` `&` `;` `|` `<` `>` `(` `)` 等）就是一次静默的
// 命令截断或参数吞没。空字符串不匹配 safeUnquotedWord（该模式要求至少一个字符），
// 自然落入单引号分支，被两个单引号包裹成空词，不需要单独的空串特判。
func QuoteIfNeeded(s string) string {
	if safeUnquotedWord.MatchString(s) {
		return s
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}
