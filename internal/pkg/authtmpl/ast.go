package authtmpl

import (
	"bytes"
	"fmt"
	"strconv"
)

// exprNode 是表达式求值树的节点。eval 的返回值是原始字节：字符串字面量是其
// UTF-8 字节，字段引用是资产上该字段的字节，函数调用按函数自身的返回值
// （哈希/HMAC 是摘要字节，编码函数是编码后的文本字节）。
type exprNode interface {
	eval(rc *RenderContext) ([]byte, error)
}

// literalNode 是一个字符串字面量，转义已经在词法阶段解码完毕。
type literalNode struct {
	value []byte
}

func (n literalNode) eval(*RenderContext) ([]byte, error) {
	return n.value, nil
}

// fieldNode 引用资产上的一个字段值。Parse 阶段已确认字段名在
// ParseOptions.Fields 里；Render 阶段若 RenderContext 没有提供该字段的值，
// 说明调用方没有按契约填充渲染上下文，这里直接报错而不是当作空串。
type fieldNode struct {
	name string
}

func (n fieldNode) eval(rc *RenderContext) ([]byte, error) {
	v, ok := rc.fields[n.name]
	if !ok {
		return nil, fmt.Errorf("authtmpl: render context missing value for field %q", n.name)
	}
	return []byte(v), nil
}

// concatNode 是 `+` 拼接的多个操作数，按字节顺序拼接。
type concatNode struct {
	parts []exprNode
}

func (n concatNode) eval(rc *RenderContext) ([]byte, error) {
	var buf bytes.Buffer
	for _, part := range n.parts {
		v, err := part.eval(rc)
		if err != nil {
			return nil, err
		}
		buf.Write(v)
	}
	return buf.Bytes(), nil
}

// funcCallNode 是一次内置函数调用，参数个数已在 Parse 阶段校验。
type funcCallNode struct {
	name string
	args []exprNode
	spec funcSpec
}

func (n funcCallNode) eval(rc *RenderContext) ([]byte, error) {
	argVals := make([][]byte, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(rc)
		if err != nil {
			return nil, err
		}
		argVals[i] = v
	}
	return n.spec.call(argVals)
}

// nowNode 是 now.unix / now.unix_ms，取 RenderContext 构造时固定下来的时刻，
// 同一个 RenderContext 内多次求值结果一致。
type nowNode struct {
	attr string // "unix" or "unix_ms"
}

func (n nowNode) eval(rc *RenderContext) ([]byte, error) {
	switch n.attr {
	case "unix":
		return []byte(strconv.FormatInt(rc.now.Unix(), 10)), nil
	case "unix_ms":
		return []byte(strconv.FormatInt(rc.now.UnixMilli(), 10)), nil
	default:
		return nil, fmt.Errorf("authtmpl: internal: unknown now attribute %q", n.attr)
	}
}

// requestNode 是 request.method / request.path / request.body，仅当 Parse 时
// AllowRequest 为 true 才会出现；Render 时若调用方没有用 WithRequest 提供请求
// 信息，说明调用方没有在允许 request.* 的位置真正传入请求，直接报错。
type requestNode struct {
	attr string // "method", "path" or "body"
}

func (n requestNode) eval(rc *RenderContext) ([]byte, error) {
	if rc.request == nil {
		return nil, fmt.Errorf("authtmpl: request.%s referenced but render context has no request info", n.attr)
	}
	switch n.attr {
	case "method":
		return []byte(rc.request.Method), nil
	case "path":
		return []byte(rc.request.Path), nil
	case "body":
		return rc.request.Body, nil
	default:
		return nil, fmt.Errorf("authtmpl: internal: unknown request attribute %q", n.attr)
	}
}
