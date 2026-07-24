package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/extension"
)

func TestParseExtCommand(t *testing.T) {
	def := extension.ToolDef{Name: "list_objects", Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bucket":  map[string]any{"type": "string"},
			"maxKeys": map[string]any{"type": "integer"},
			"keys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"force":   map[string]any{"type": "boolean"},
			"ratio":   map[string]any{"type": "number"},
		},
		"required": []any{"bucket"},
	}}

	t.Run("按声明类型转换", func(t *testing.T) {
		// cmdline 的 flag 语法是 --k=v 或裸 --k（见 internal/ai/cmdline 文档注释），
		// 不是 --k v 的空格分隔式；与 mongo/kafka DSL 同一种约定。
		ext, tool, argsJSON, err := parseExtCommand(`oss list_objects --bucket=my-bucket --maxKeys=100 --force`, def)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if ext != "oss" || tool != "list_objects" {
			t.Fatalf("got (%q, %q), want (oss, list_objects)", ext, tool)
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
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --bucket=b --ratio=3.14`, def)
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
		_, _, _, err := parseExtCommand(`oss list_objects --bucket=b --ratio=abc`, def)
		if err == nil || !strings.Contains(err.Error(), "ratio") {
			t.Fatalf("a bad number must name the flag, got %v", err)
		}
	})

	t.Run("array<string> 按逗号切分", func(t *testing.T) {
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --bucket=b --keys=a,b,c`, def)
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
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --json='{"bucket":"b","keys":["a,b","c"]}'`, def)
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

	t.Run("--json 与其它 flag 混用报错", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --json='{"bucket":"b"}' --force`, def)
		if err == nil || !strings.Contains(err.Error(), "json") {
			t.Fatalf("--json combined with other flags must be rejected and name --json, got %v", err)
		}
	})

	t.Run("未声明的 flag 报错并点名", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --nope=1`, def)
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("an undeclared flag must be named in the error, got %v", err)
		}
	})

	t.Run("类型不符报错并点名类型", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --maxKeys=abc`, def)
		if err == nil || !strings.Contains(err.Error(), "integer") {
			t.Fatalf("a bad integer must say so, got %v", err)
		}
	})

	t.Run("缺少工具名报错", func(t *testing.T) {
		if _, _, _, err := parseExtCommand(`oss`, def); err == nil {
			t.Fatal("a command without a tool name must fail")
		}
	})

	t.Run("多余的位置参数报错", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects extra --bucket=b`, def)
		if err == nil || !strings.Contains(err.Error(), "extra") {
			t.Fatalf("an unexpected positional argument must be named in the error, got %v", err)
		}
	})

	t.Run("缺少 required 参数在调用期拒绝", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --force`, def)
		if err == nil || !strings.Contains(err.Error(), "bucket") || !strings.Contains(err.Error(), "required") {
			t.Fatalf("missing required bucket = %v, want an explicit required-parameter error", err)
		}
	})

	t.Run("--json 必须是 object", func(t *testing.T) {
		for _, raw := range []string{`[]`, `7`, `null`, `"text"`} {
			_, _, _, err := parseExtCommand(`oss list_objects --json='`+raw+`'`, def)
			if err == nil || !strings.Contains(err.Error(), "object") {
				t.Fatalf("--json=%s error = %v, want object-shape rejection", raw, err)
			}
		}
	})

	t.Run("--json 拒绝未知参数", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --json='{"bucket":"b","ghost":1}'`, def)
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("unknown JSON parameter = %v, want rejection naming ghost", err)
		}
	})

	t.Run("--json 拒绝重复参数", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --json='{"bucket":"a","bucket":"b"}'`, def)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("duplicate JSON parameter = %v, want rejection", err)
		}
	})

	t.Run("--json 校验声明类型", func(t *testing.T) {
		_, _, _, err := parseExtCommand(`oss list_objects --json='{"bucket":"b","maxKeys":"100"}'`, def)
		if err == nil || !strings.Contains(err.Error(), "maxKeys") || !strings.Contains(err.Error(), "integer") {
			t.Fatalf("wrong JSON parameter type = %v, want rejection", err)
		}
	})
}
