package etcd_svc

import "testing"

// FuzzParseFormat 锁住 ParseCommand/FormatCommand 的互逆性质，作为举例式
// round-trip 用例（TestParseFormat_RoundTrip）的兜底：
//
//  1. ParseCommand 对任意字符串都不能 panic，只能返回请求或干净的错误；
//  2. 一个能被解析的命令串，渲染出来必须还能被解析回去，且再渲染一次不变
//     （FormatCommand∘ParseCommand 是幂等的）。
//
// 性质 2 正是"{Op:get, Prefix:true} 渲染成 get --prefix、读回来却报
// get requires a key"这类断裂的判据：渲染结果同时是策略匹配、审批文本与
// SaveGrantPattern 用的字符串，它读不回来就意味着批准的文本与执行的动作分叉。
func FuzzParseFormat(f *testing.F) {
	seeds := []string{
		"get /app/config",
		"get /app/ --prefix",
		"get '' --prefix",
		"get '' --limit=10",
		"get /app/config --limit=10 --revision=42",
		"put /app/config hello",
		"put /app/config 'hello world'",
		"put '' v",
		"put /k ''",
		"del /app/ --prefix",
		"del '' --prefix",
		"del --prefix",
		"del",
		"get",
		"lease grant --ttl=3600",
		"lease revoke --lease=694d5c0f",
		"lease list",
		"member list",
		"endpoint status",
		"endpoint health 127.0.0.1:2379",
		"get '/prod/配置'",
		"put '/prod/my key' 'my value'",
		"get /app --nonsense=1",
		"get '--prefix'",
		"",
		"   ",
		"get 'unterminated",
		"a | b",
		"get $(x)",
		"GET /K",
		"get -x",
		"lease grant --ttl=abc",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		req, err := ParseCommand(s)
		if err != nil {
			return
		}
		rendered := FormatCommand(req)
		reparsed, err := ParseCommand(rendered)
		if err != nil {
			t.Fatalf("ParseCommand(%q) ok, but its FormatCommand output %q fails to parse: %v", s, rendered, err)
		}
		if again := FormatCommand(reparsed); again != rendered {
			t.Fatalf("FormatCommand not idempotent for %q: %q → %q", s, rendered, again)
		}
	})
}
