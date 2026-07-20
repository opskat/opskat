// Package cmdline 提供引号感知的命令行切词与 flag 解析，供统一 exec 下各资产类型的
// 命令 DSL 共用（mongo / etcd / kafka），以及 k8s 的 kubectl 参数解析。
//
// 只做词法层，不认识任何具体协议的动词：谁是 verb、哪些 flag 合法，由各类型的
// <name>_command.go 自己判断。
package cmdline

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Words 把命令串切成词，保留引号内的空格，并剥掉引号本身。
//
// 拒绝一切 shell 控制结构（管道、重定向、变量赋值、多语句）：这些串最终会被送去
// 执行 typed service 调用，不经过 shell，语义上没有"管道"这回事；容忍它们只会让
// 模型写出看似成功、实则参数被吃掉的命令。
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
		if lit != "" {
			words = append(words, lit)
		}
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

// Render 把 Command 还原为命令串。Flags 按名称排序输出，保证同一个 Command
// 渲染结果稳定——Go map 迭代顺序随机，不排序的话 Render 不是函数。
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

// QuoteIfNeeded 在值含空格或引号时加单引号；值本身含单引号时退化为双引号包裹。
// 与 Words 的剥引号逻辑互逆——两者必须一起改。
func QuoteIfNeeded(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`") {
		return s
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}
