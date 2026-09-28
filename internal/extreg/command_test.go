package extreg

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/extension"
)

func TestParseCommand(t *testing.T) {
	// 扩展名不再出现在命令里：资产类型 → 扩展是一对一的，工具名就是第一个词。
	m := &extension.Manifest{Name: "oss", Tools: []extension.ToolDef{{
		Name: "list_objects", Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bucket":  map[string]any{"type": "string"},
				"maxKeys": map[string]any{"type": "integer"},
				"keys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"force":   map[string]any{"type": "boolean"},
				"ratio":   map[string]any{"type": "number"},
			},
			"required": []any{"bucket"},
		},
	}}}

	t.Run("按声明类型转换", func(t *testing.T) {
		// cmdline 的默认 flag 语法是 --k=v 或裸 --k（见 internal/ai/cmdline 文档注释），
		// mongo/kafka DSL 就是这个默认语义；扩展工具 DSL 在此基础上另按参数声明类型
		// 接受 --k v 空格分隔式（见下面的 "按声明类型接受 --flag value" 用例）。
		tool, argsJSON, err := parseCommand(m, `list_objects --bucket=my-bucket --maxKeys=100 --force`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if tool != "list_objects" {
			t.Fatalf("tool = %q, want list_objects", tool)
		}
		var got map[string]any
		if err := json.Unmarshal(argsJSON, &got); err != nil {
			t.Fatalf("args must be valid JSON: %v", err)
		}
		// integer 必须是数字而不是字符串——WASM 侧按 schema 解码，"100" 会解失败
		if got["maxKeys"] != float64(100) {
			t.Errorf("maxKeys = %#v, want 100 (number, not string)", got["maxKeys"])
		}
		if got["force"] != true {
			t.Errorf("bare boolean flag = %#v, want true", got["force"])
		}
		if got["bucket"] != "my-bucket" {
			t.Errorf("bucket = %#v", got["bucket"])
		}
	})

	t.Run("number 按浮点数转换", func(t *testing.T) {
		_, argsJSON, err := parseCommand(m, `list_objects --bucket=b --ratio=3.14`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(argsJSON, &got); err != nil {
			t.Fatalf("args must be valid JSON: %v", err)
		}
		if got["ratio"] != 3.14 {
			t.Errorf("ratio = %#v, want 3.14 (number, not string)", got["ratio"])
		}
	})

	t.Run("number 类型不符报错并点名 flag", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --bucket=b --ratio=abc`)
		if err == nil || !strings.Contains(err.Error(), "ratio") {
			t.Fatalf("a bad number must name the flag, got %v", err)
		}
	})

	t.Run("按声明类型接受 --flag value 空格分隔式", func(t *testing.T) {
		// 非 boolean 声明的 flag 消费下一个词作为其值——opsctl 的 flag DSL 因此不必
		// 强制模型总是写 --k=v，与 ES 查询串这类含 = 的值共存时更不容易写错。
		_, argsJSON, err := parseCommand(m, `list_objects --bucket my-bucket --maxKeys 100`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(argsJSON, &got); err != nil {
			t.Fatalf("args must be valid JSON: %v", err)
		}
		if got["bucket"] != "my-bucket" {
			t.Errorf("bucket = %#v, want my-bucket", got["bucket"])
		}
		if got["maxKeys"] != float64(100) {
			t.Errorf("maxKeys = %#v, want 100 (number, not string)", got["maxKeys"])
		}
	})

	t.Run("boolean flag 不吞下一个词", func(t *testing.T) {
		// --force 是 boolean：空格后的词必须留作独立位置参数（进而被拒），而不是被
		// --force 当成自己的值吃掉。
		_, _, err := parseCommand(m, `list_objects --bucket=b --force extra`)
		if err == nil || !strings.Contains(err.Error(), "unexpected positional argument") {
			t.Fatalf("a boolean flag must not swallow the next word, got %v", err)
		}
	})

	t.Run("boolean flag 仍支持 --flag=false 显式赋值", func(t *testing.T) {
		_, argsJSON, err := parseCommand(m, `list_objects --bucket=b --force=false`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(argsJSON, &got); err != nil {
			t.Fatalf("args must be valid JSON: %v", err)
		}
		if got["force"] != false {
			t.Errorf("force = %#v, want false", got["force"])
		}
	})

	t.Run("非 boolean flag 空格形式缺值报错", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --bucket=b --maxKeys`)
		if err == nil {
			t.Fatalf("a value-taking flag with nothing following it must be rejected")
		}
	})

	t.Run("array<string> 按逗号切分", func(t *testing.T) {
		_, argsJSON, err := parseCommand(m, `list_objects --bucket=b --keys=a,b,c`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		_ = json.Unmarshal(argsJSON, &got)
		keys, ok := got["keys"].([]any)
		if !ok || len(keys) != 3 {
			t.Fatalf("keys = %#v, want a 3-element array", got["keys"])
		}
	})

	t.Run("--json 逃生口整体接管", func(t *testing.T) {
		_, argsJSON, err := parseCommand(m, `list_objects --json='{"bucket":"b","keys":["a,b","c"]}'`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		_ = json.Unmarshal(argsJSON, &got)
		keys, ok := got["keys"].([]any)
		if !ok || len(keys) != 2 || keys[0] != "a,b" {
			t.Errorf("--json must preserve declared values the comma flag syntax cannot express, got %#v", got["keys"])
		}
	})

	t.Run("--json 同样接受空格分隔式", func(t *testing.T) {
		// 多词 opsctl exec 与 --flag value 写法对 --json 一视同仁：
		// `opsctl exec <asset> -- list_objects --json '{...}'` 不能掉成"多余位置参数"。
		_, argsJSON, err := parseCommand(m, `list_objects --json '{"bucket":"b","maxKeys":3}'`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(argsJSON, &got); err != nil {
			t.Fatalf("args must be valid JSON: %v", err)
		}
		if got["bucket"] != "b" || got["maxKeys"] != float64(3) {
			t.Errorf("args = %#v, want bucket=b maxKeys=3", got)
		}
	})

	t.Run("--json 与其它 flag 混用报错", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --json='{"bucket":"b"}' --force`)
		if err == nil || !strings.Contains(err.Error(), "json") {
			t.Fatalf("--json combined with other flags must be rejected and name --json, got %v", err)
		}
	})

	t.Run("未声明的 flag 报错并点名", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --nope=1`)
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("an undeclared flag must be named in the error, got %v", err)
		}
	})

	t.Run("类型不符报错并点名类型", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --maxKeys=abc`)
		if err == nil || !strings.Contains(err.Error(), "integer") {
			t.Fatalf("a bad integer must say so, got %v", err)
		}
	})

	t.Run("未知工具名报错", func(t *testing.T) {
		if _, _, err := parseCommand(m, `nope`); err == nil {
			t.Fatal("an unknown tool name must fail")
		}
	})

	t.Run("多余的位置参数报错", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects extra --bucket=b`)
		if err == nil || !strings.Contains(err.Error(), "extra") {
			t.Fatalf("an unexpected positional argument must be named in the error, got %v", err)
		}
	})

	t.Run("缺少 required 参数在调用期拒绝", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --force`)
		if err == nil || !strings.Contains(err.Error(), "bucket") || !strings.Contains(err.Error(), "required") {
			t.Fatalf("missing required bucket = %v, want an explicit required-parameter error", err)
		}
	})

	t.Run("--json 必须是 object", func(t *testing.T) {
		for _, raw := range []string{`[]`, `7`, `null`, `"text"`} {
			_, _, err := parseCommand(m, `list_objects --json='`+raw+`'`)
			if err == nil || !strings.Contains(err.Error(), "object") {
				t.Fatalf("--json=%s error = %v, want object-shape rejection", raw, err)
			}
		}
	})

	t.Run("--json 拒绝未知参数", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --json='{"bucket":"b","ghost":1}'`)
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("unknown JSON parameter = %v, want rejection naming ghost", err)
		}
	})

	t.Run("--json 拒绝重复参数", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --json='{"bucket":"a","bucket":"b"}'`)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("duplicate JSON parameter = %v, want rejection", err)
		}
	})

	t.Run("--json 校验声明类型", func(t *testing.T) {
		_, _, err := parseCommand(m, `list_objects --json='{"bucket":"b","maxKeys":"100"}'`)
		if err == nil || !strings.Contains(err.Error(), "maxKeys") || !strings.Contains(err.Error(), "integer") {
			t.Fatalf("wrong JSON parameter type = %v, want rejection", err)
		}
	})
}

// The canonical command is re-parsed by every later stage (policy, classify,
// execute), so it must parse back to exactly the arguments it was rendered from —
// including a string or array value that happens to be the literal "true", which
// must not come out as a bare flag that the space-separated form then reads as
// "take the next word".
func TestCanonicalCommandParsesBackToTheSameArguments(t *testing.T) {
	m := &extension.Manifest{Name: "notes", Tools: []extension.ToolDef{{
		Name: "put", Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a":     map[string]any{"type": "string"},
				"b":     map[string]any{"type": "string"},
				"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"force": map[string]any{"type": "boolean"},
			},
		},
	}}}
	for _, command := range []string{
		`put --a=true --b=x`,
		`put --b=true`,
		`put --tags=true --a=y`,
		`put --force --a=true`,
		// Values the flag DSL cannot spell: the canonical form policy, approval and
		// grant judge must still be the arguments that run.
		`put --json='{"tags":["a,b"],"a":"x"}'`,
		`put --json='{"tags":[]}'`,
	} {
		t.Run(command, func(t *testing.T) {
			_, want, err := parseCommand(m, command)
			if err != nil {
				t.Fatalf("parse %q: %v", command, err)
			}
			canonical, err := canonicalCommand(m, command)
			if err != nil {
				t.Fatalf("canonicalize %q: %v", command, err)
			}
			_, got, err := parseCommand(m, canonical)
			if err != nil {
				t.Fatalf("canonical %q does not parse back: %v", canonical, err)
			}
			if string(got) != string(want) {
				t.Fatalf("canonical %q parses to %s, want %s", canonical, got, want)
			}
		})
	}
}
