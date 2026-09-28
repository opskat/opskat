// Package authtmpl 实现自定义类型的模板语言：`{{ 表达式 }}` 出现在 HTTP
// 认证绑定、命令模板与环境变量绑定三处，共享同一套语法与函数实现（见
// docs/specs/2026-09-28-generic-asset.md 「模板语言」一节，Design decision 18）。
//
// 求值时的中间值一律是原始字节（哈希/HMAC 返回摘要字节，未经 base64/hex 编码），
// 拼接按字节拼接；最终 Render 结果转换为字符串供调用方写入 header / 环境变量 /
// 命令参数。渲染过程不记录任何日志，调用方也不应记录 Render 的返回值。
package authtmpl

import (
	"bytes"
	"fmt"
	"strings"
)

// ParseOptions 描述 Parse 时的静态校验上下文。
type ParseOptions struct {
	// Fields 是允许被模板引用的字段名（自定义类型的字段结构）。
	// 引用了不在这个列表里的标识符会在 Parse 阶段报错。
	Fields []string
	// AllowRequest 为 true 时才允许使用 request.method / request.path / request.body；
	// 命令模板与环境变量绑定必须传 false（这些位置不存在“请求”）。
	AllowRequest bool
}

// Template 是解析后的模板，可以反复 Render，Render 之间互不影响。
type Template struct {
	parts []segment
}

// segment 是模板里一段可求值的片段：要么是原样输出的字面文本，
// 要么是一个 `{{ 表达式 }}`。
type segment struct {
	literal []byte // literal != nil 时，expr 必须为 nil
	expr    exprNode
}

// Parse 解析模板源码。src 里的 `{{ 表达式 }}` 会被解析为表达式，
// 其余部分原样作为字面文本。所有语法错误、未知字段、未知函数、
// 参数个数不对、以及在不允许的位置使用 request.* 都会在这里报错，
// 而不是留到 Render 时才发现。
func Parse(src string, opts ParseOptions) (*Template, error) {
	fieldSet := make(map[string]struct{}, len(opts.Fields))
	for _, f := range opts.Fields {
		fieldSet[f] = struct{}{}
	}

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
		end := strings.Index(rest, "}}")
		if end < 0 {
			return nil, fmt.Errorf("authtmpl: unterminated expression (missing '}}')")
		}
		exprSrc := rest[:end]
		rest = rest[end+2:]

		node, err := parseExpr(exprSrc, fieldSet, opts.AllowRequest)
		if err != nil {
			return nil, fmt.Errorf("authtmpl: %w (in {{%s}})", err, exprSrc)
		}
		parts = append(parts, segment{expr: node})
	}

	return &Template{parts: parts}, nil
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
