package aictx

import (
	"encoding/json"
	"testing"
)

// TestArgInt64 锁住 string 分支。缺了它,统一 exec 的命令 DSL 里每一个数值 flag
// （--limit / --partitions / --offset,值天然是字符串）都会静默变成 0——不报错,
// 而是给出与用户批准的那条命令不同的结果。
func TestArgInt64(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want int64
	}{
		{name: "float64 from JSON", args: map[string]any{"n": float64(42)}, want: 42},
		{name: "int", args: map[string]any{"n": 42}, want: 42},
		{name: "int64", args: map[string]any{"n": int64(42)}, want: 42},
		{name: "json.Number", args: map[string]any{"n": json.Number("42")}, want: 42},
		{name: "string", args: map[string]any{"n": "42"}, want: 42},
		{name: "negative string", args: map[string]any{"n": "-42"}, want: -42},
		{name: "padded string", args: map[string]any{"n": " 42 "}, want: 42},
		{name: "non-numeric string", args: map[string]any{"n": "abc"}, want: 0},
		{name: "empty string", args: map[string]any{"n": ""}, want: 0},
		{name: "wrong type", args: map[string]any{"n": true}, want: 0},
		{name: "missing", args: map[string]any{}, want: 0},
		{name: "nil args", args: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ArgInt64(tt.args, "n"); got != tt.want {
				t.Fatalf("ArgInt64() = %d, want %d", got, tt.want)
			}
			if got := ArgInt(tt.args, "n"); got != int(tt.want) {
				t.Fatalf("ArgInt() = %d, want %d", got, int(tt.want))
			}
		})
	}
}

func TestArgBool(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want bool
	}{
		{name: "native true", args: map[string]any{"flag": true}, want: true},
		{name: "native false", args: map[string]any{"flag": false}, want: false},
		{name: "trimmed string true", args: map[string]any{"flag": " true "}, want: true},
		{name: "case insensitive string true", args: map[string]any{"flag": "TRUE"}, want: true},
		{name: "string false", args: map[string]any{"flag": "FALSE"}, want: false},
		{name: "invalid string", args: map[string]any{"flag": "yes"}, want: false},
		{name: "wrong type", args: map[string]any{"flag": 1}, want: false},
		{name: "missing", args: map[string]any{}, want: false},
		{name: "nil args", args: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ArgBool(tt.args, "flag"); got != tt.want {
				t.Fatalf("ArgBool() = %v, want %v", got, tt.want)
			}
		})
	}
}
