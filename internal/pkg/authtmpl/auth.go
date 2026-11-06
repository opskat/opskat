package authtmpl

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
			req.URL.RawQuery = setQueryParam(req.URL.RawQuery, name, values[0])
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

// setQueryParam 把 name=value 写进原始 query：去掉同名的已有参数，再追加在末尾。其余部分
// 按调用方写的原样保留——url.Values 往返会丢掉 ParseQuery 不认识的写法（`;` 分隔、坏的 %
// 转义）并把无值参数 `flag` 改写成 `flag=`。
func setQueryParam(rawQuery, name, value string) string {
	var kept []string
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		key, _, _ := strings.Cut(pair, "=")
		if k, err := url.QueryUnescape(key); err == nil {
			key = k
		}
		if key == name {
			continue
		}
		kept = append(kept, pair)
	}
	return strings.Join(append(kept, url.QueryEscape(name)+"="+url.QueryEscape(value)), "&")
}

// AuthTypeFor 按标识查找内置认证类型，ok 为 false 表示未注册（例如类型配置
// 里引用了尚未支持的签名器）。
func AuthTypeFor(kind string) (AuthType, bool) {
	at, ok := builtinAuthTypes[kind]
	return at, ok
}
