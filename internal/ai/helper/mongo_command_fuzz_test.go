package helper

import "testing"

// FuzzParseMongoCommandRoundTrip 锁住 ParseMongoCommand/Render 的互逆性质，作为
// TestMongoCommand_RoundTrip 举例式用例的兜底，同源于
// internal/service/etcd_svc.FuzzParseFormat：
//
//  1. ParseMongoCommand 对任意字符串都不能 panic，只能返回命令或干净的错误；
//  2. 一个能被解析的命令串，渲染出来必须还能被解析回去，且再解析一次得到相同的
//     MongoCommand（Parse∘Render∘Parse 是幂等的）。
//
// 这条性质正是 mongo query 是 JSON blob（含 {, }, ", $, 空格, 单引号）这一点最容易
// 打破的地方：canonical 串同时是策略匹配、审批弹窗文本与审计记录——渲染丢一个字段
// 或转义错一个字符，用户批准的和实际执行的就分叉了。
func FuzzParseMongoCommandRoundTrip(f *testing.F) {
	seeds := []string{
		"find users",
		`find users --query='{"filter":{"age":{"$gt":21}}}'`,
		"find users --db=analytics",
		"listDatabases",
		`deleteMany logs --query='{"filter":{"level":"debug"}}'`,
		`aggregate events --query='{"pipeline":[{"$match":{"x":"a b"}}]}'`,
		`insertOne users --query='{"document":{"name":"o'"'"'brien"}}'`,
		"listCollections --db=analytics",
		"find",
		"dropEverything users",
		"find a b",
		`find users --db=analytics --query='{"filter":{"a":1}}'`,
		"find users --bogus=1",
		"",
		"   ",
		"find 'unterminated",
		"a | b",
		"find $(x)",
		"FIND users",
		"find users --query=",
		"find '' --db=''",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		cmd, err := ParseMongoCommand(s)
		if err != nil {
			return
		}
		rendered, err := cmd.Render()
		if err != nil {
			// A MongoCommand produced by ParseMongoCommand never has a Collection
			// starting with "--" (Words+Parse would have consumed such a word as a
			// flag, not a positional), so Render should never reject it. See
			// TestMongoCommand_RenderRejectsFlagLikeCollection for the struct-side
			// input Parse can never produce.
			t.Fatalf("ParseMongoCommand(%q) ok as %#v, but Render rejected it: %v", s, *cmd, err)
		}
		reparsed, err := ParseMongoCommand(rendered)
		if err != nil {
			t.Fatalf("ParseMongoCommand(%q) ok as %#v, but its Render output %q fails to parse: %v", s, *cmd, rendered, err)
		}
		if *reparsed != *cmd {
			t.Fatalf("round-trip mismatch for %q: parsed %#v, rendered %q, reparsed %#v", s, *cmd, rendered, *reparsed)
		}
		if again, err := reparsed.Render(); err != nil || again != rendered {
			t.Fatalf("Render not idempotent for %q: %q -> %q (err: %v)", s, rendered, again, err)
		}
	})
}
