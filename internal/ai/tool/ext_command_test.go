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
		},
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

	t.Run("array<string> 按逗号切分", func(t *testing.T) {
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --keys=a,b,c`, def)
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
		_, _, argsJSON, err := parseExtCommand(`oss list_objects --json='{"bucket":"b","nested":{"k":1}}'`, def)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got map[string]any
		_ = json.Unmarshal(argsJSON, &got)
		if got["nested"] == nil {
			t.Error("--json must pass through shapes the flag DSL cannot express")
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
}
