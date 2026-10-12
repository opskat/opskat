// Package authtmpl 实现自定义类型的模板语言：`{{ 表达式 }}` 出现在 HTTP
// 认证绑定、命令模板与环境变量绑定三处，共享同一套语法与函数实现（见
// docs/specs/2026-09-28-generic-asset.md 「模板语言」一节，Design decision 18）。
// 扩展 auth 绑定的值模板（pkg/extension）也由这个包解析与渲染：语法是扩展既有
// 写法的超集（Design decision 19），已发布扩展的模板必须原样可用。
//
// 求值时的中间值一律是原始字节（哈希/HMAC 返回摘要字节，未经 base64/hex 编码），
// 拼接按字节拼接；最终 Render 结果转换为字符串供调用方写入 header / 环境变量 /
// 命令参数。渲染过程不记录任何日志，调用方也不应记录 Render 的返回值。
package authtmpl

import (
	"bytes"
	"strings"
)

// ParseOptions 描述 Parse 时的静态校验上下文。
type ParseOptions struct {
	// Fields 是允许被模板引用的字段名（自定义类型的字段结构）。
	// 引用了不在这个列表里的标识符会在 Parse 阶段报错。
	Fields []string
	// AnyField 为 true 时不按 Fields 校验字段引用：任何标识符都按字段解析，由
	// 调用方拿 Template.Fields() 对照自己的字段契约（扩展 auth 绑定对照的是
	// 资产类型的 configSchema）。
	AnyField bool
	// AllowRequest 为 true 时才允许使用 request.method / request.path / request.body；
	// 命令模板与环境变量绑定必须传 false（这些位置不存在“请求”）。
	AllowRequest bool
}

// Template 是解析后的模板，可以反复 Render，Render 之间互不影响。
type Template struct {
	parts  []segment
	fields []string
}

// Fields 返回模板引用到的字段名，按首次出现的顺序，不重复。函数名与
// now.* / request.* 不是字段，不在其中。
func (t *Template) Fields() []string {
	return t.fields
}

// segment 是模板里一段可求值的片段：要么是原样输出的字面文本，
// 要么是一个 `{{ 表达式 }}`。
type segment struct {
	literal []byte // literal != nil 时，expr 必须为 nil
	expr    exprNode
}

// Parse 解析模板源码。src 里的 `{{ 表达式 }}` 会被解析为表达式，
// 其余部分原样作为字面文本。所有语法错误、未知字段、未知函数、
// 参数个数不对、以及在不允许的位置使用 request.* 都会在这里报错（*ParseError），
// 而不是留到 Render 时才发现。
func Parse(src string, opts ParseOptions) (*Template, error) {
	sc := newScope(opts)

	var parts []segment
	rest := src
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			if len(rest) > 0 {
				parts = append(parts, segment{literal: []byte(rest)})
			}
			break
		}
		if start > 0 {
			parts = append(parts, segment{literal: []byte(rest[:start])})
		}
		rest = rest[start+2:]
		end := exprEnd(rest)
		if end < 0 {
			return nil, newParseError(CodeUnterminatedExpression, "unterminated expression (missing '}}')")
		}
		exprSrc := rest[:end]
		rest = rest[end+2:]

		node, perr := parseExpr(exprSrc, sc)
		if perr != nil {
			perr.Params["expr"] = strings.TrimSpace(exprSrc)
			return nil, perr
		}
		parts = append(parts, segment{expr: node})
	}

	return &Template{parts: parts, fields: sc.refs}, nil
}

// exprEnd 返回结束一个表达式的 `}}` 在 s 里的位置，没有则返回 -1。字符串
// 字面量里的 `}}` 不算结束。字面量没有闭合时按第一个 `}}` 截断，让词法阶段
// 把它报成未闭合的字符串，而不是未闭合的表达式。
func exprEnd(s string) int {
	inString := false
	for i := 0; i < len(s); i++ {
		switch {
		case inString && s[i] == '\\':
			i++
		case s[i] == '"':
			inString = !inString
		case !inString && strings.HasPrefix(s[i:], "}}"):
			return i
		}
	}
	return strings.Index(s, "}}")
}

// Render 用 rc 携带的字段值 / 固定 now / 可选 request 信息对模板求值，
// 返回渲染后的字符串。同一个 rc 内多次引用 now.unix / now.unix_ms
// 得到的是同一个时刻（rc 构造时取的那一次）。
func (t *Template) Render(rc *RenderContext) (string, error) {
	var buf bytes.Buffer
	for _, p := range t.parts {
		if p.expr == nil {
			buf.Write(p.literal)
			continue
		}
		v, err := p.expr.eval(rc)
		if err != nil {
			return "", err
		}
		buf.Write(v)
	}
	return buf.String(), nil
}
