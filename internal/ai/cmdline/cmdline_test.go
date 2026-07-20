package cmdline

import (
	"reflect"
	"testing"
)

func TestWords_QuoteAware(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`topic create orders`, []string{"topic", "create", "orders"}},
		{`message produce t --value='{"a": 1}'`, []string{"message", "produce", "t", `--value={"a": 1}`}},
		{`message produce t --value="hello world"`, []string{"message", "produce", "t", "--value=hello world"}},
		{`get /app/config --prefix`, []string{"get", "/app/config", "--prefix"}},
	}
	for _, c := range cases {
		got, err := Words(c.in)
		if err != nil {
			t.Fatalf("Words(%q) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("Words(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestWords_RejectsShellControl(t *testing.T) {
	cases := []string{
		`topic list | grep x`,
		`topic list > /tmp/out`,
		`FOO=bar topic list`,
		`topic list; rm -rf /`,
	}
	for _, in := range cases {
		if _, err := Words(in); err == nil {
			t.Fatalf("Words(%q) = nil error, want rejection", in)
		}
	}
}

func TestParse_VerbArgsFlags(t *testing.T) {
	got, err := Parse(`topic create orders --partitions=3 --dry-run`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Verb != "topic" {
		t.Fatalf("Verb = %q, want %q", got.Verb, "topic")
	}
	if !reflect.DeepEqual(got.Args, []string{"create", "orders"}) {
		t.Fatalf("Args = %#v, want %#v", got.Args, []string{"create", "orders"})
	}
	want := map[string]string{"partitions": "3", "dry-run": "true"}
	if !reflect.DeepEqual(got.Flags, want) {
		t.Fatalf("Flags = %#v, want %#v", got.Flags, want)
	}
}

// TestParseRender_RoundTrip 是本 Plan 的核心属性：Parse(Render(c)) == c。
// 生成输入而非固定表——手写表只会覆盖作者想到的情形，而引号/空格/JSON 恰恰是想不到的地方。
func TestParseRender_RoundTrip(t *testing.T) {
	verbs := []string{"topic", "message", "get", "find"}
	argSets := [][]string{
		nil,
		{"create"},
		{"create", "orders"},
		{"produce", "orders-2024"},
	}
	flagSets := []map[string]string{
		nil,
		{"prefix": "true"},
		{"partitions": "3", "replication-factor": "2"},
		{"value": `{"a": 1, "b": "x y"}`},
		{"value": "hello world", "key": "k1"},
		{"filter": `{"name": "o'brien"}`},
	}

	for _, verb := range verbs {
		for _, args := range argSets {
			for _, flags := range flagSets {
				original := &Command{Verb: verb, Args: args, Flags: flags}
				rendered := original.Render()
				reparsed, err := Parse(rendered)
				if err != nil {
					t.Fatalf("Parse(Render(%#v)) = error %v (rendered: %s)", original, err, rendered)
				}
				if reparsed.Verb != verb {
					t.Fatalf("round-trip Verb = %q, want %q (rendered: %s)", reparsed.Verb, verb, rendered)
				}
				if len(reparsed.Args) != len(args) {
					t.Fatalf("round-trip Args = %#v, want %#v (rendered: %s)", reparsed.Args, args, rendered)
				}
				for i := range args {
					if reparsed.Args[i] != args[i] {
						t.Fatalf("round-trip Args[%d] = %q, want %q (rendered: %s)", i, reparsed.Args[i], args[i], rendered)
					}
				}
				if len(reparsed.Flags) != len(flags) {
					t.Fatalf("round-trip Flags = %#v, want %#v (rendered: %s)", reparsed.Flags, flags, rendered)
				}
				for k, v := range flags {
					if reparsed.Flags[k] != v {
						t.Fatalf("round-trip Flags[%q] = %q, want %q (rendered: %s)", k, reparsed.Flags[k], v, rendered)
					}
				}
			}
		}
	}
}
