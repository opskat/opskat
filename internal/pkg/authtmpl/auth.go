package authtmpl

import (
	"fmt"
	"net/http"
)

// AuthType 是一种认证方式的应用逻辑：把渲染好的模板值写回 *http.Request。
// 首批内置 header / query / basic（Design decision 5）。header 与 query 各
// 需要一个「名称 + 一个值模板」；basic 不需要名称，需要「用户名、密码」两个
// 值模板。ValueCount 与 HasName 供调用方（类型编辑器、执行器）在渲染前就
// 知道该认证类型要几个模板、要不要名称。
type AuthType struct {
	// Kind 是注册用的标识，例如 "header" / "query" / "basic"。
	Kind string
	// ValueCount 是 Apply 期望的 values 参数长度。
	ValueCount int
	// HasName 为 true 表示这个认证类型使用 name 参数（header 的头名、query
	// 的参数名）；basic 没有名称，HasName 为 false 时 Apply 忽略 name。
	HasName bool

	apply func(req *http.Request, name string, values []string) error
}

// Apply 把渲染后的值应用到 req 上。values 的长度必须等于 ValueCount，否则
// 返回错误而不是越界访问或悄悄丢弃多余值。
func (a AuthType) Apply(req *http.Request, name string, values []string) error {
	if len(values) != a.ValueCount {
		return fmt.Errorf("authtmpl: auth type %q expects %d value(s), got %d", a.Kind, a.ValueCount, len(values))
	}
	return a.apply(req, name, values)
}

var builtinAuthTypes = map[string]AuthType{
	"header": {
		Kind: "header", ValueCount: 1, HasName: true,
		apply: func(req *http.Request, name string, values []string) error {
			req.Header.Set(name, values[0])
			return nil
		},
	},
	"query": {
		Kind: "query", ValueCount: 1, HasName: true,
		apply: func(req *http.Request, name string, values []string) error {
			q := req.URL.Query()
			q.Set(name, values[0])
			req.URL.RawQuery = q.Encode()
			return nil
		},
	},
	"basic": {
		Kind: "basic", ValueCount: 2, HasName: false,
		apply: func(req *http.Request, _ string, values []string) error {
			req.SetBasicAuth(values[0], values[1])
			return nil
		},
	},
}

// AuthTypeFor 按标识查找内置认证类型，ok 为 false 表示未注册（例如类型配置
// 里引用了尚未支持的签名器）。
func AuthTypeFor(kind string) (AuthType, bool) {
	at, ok := builtinAuthTypes[kind]
	return at, ok
}
