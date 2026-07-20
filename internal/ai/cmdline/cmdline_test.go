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

// TestWords_EmptyWordPreserved locks in IMPORTANT-1's fix: a deliberately
// quoted empty word (single- or double-quoted, e.g. `""`) must survive
// tokenization instead of being silently dropped — otherwise it is
// unrecoverable and Parse(Render(c)) != c for any Command holding an empty
// Arg or flag value.
func TestWords_EmptyWordPreserved(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`put ''`, []string{"put", ""}},
		{`put ""`, []string{"put", ""}},
		{`put '' k`, []string{"put", "", "k"}},
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
		`topic list &`,
		`! topic list`,
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

// TestParse_RejectsDuplicateFlag pins the duplicate-flag guard in Parse:
// deleting it would let a later --k=v silently overwrite an earlier one
// instead of surfacing the ambiguous input to the caller.
func TestParse_RejectsDuplicateFlag(t *testing.T) {
	_, err := Parse(`topic create --partitions=3 --partitions=5`)
	if err == nil {
		t.Fatalf("Parse duplicate flag = nil error, want rejection")
	}
}

// TestParse_RejectsMalformedFlag pins the malformed-flag guard in Parse:
// "--=x" has an empty flag name and must be rejected rather than silently
// accepted as a flag named "".
func TestParse_RejectsMalformedFlag(t *testing.T) {
	_, err := Parse(`topic create --=x`)
	if err == nil {
		t.Fatalf("Parse malformed flag = nil error, want rejection")
	}
}

// TestQuoteIfNeeded_MetacharactersForceQuoting is the direct regression test
// for the CRITICAL finding: QuoteIfNeeded used to allowlist-miss shell
// metacharacters (# & ; | < > ( )) and leave them unquoted, which Words then
// re-interpreted as comments/control structures on re-parse — silent
// corruption, not a parse error. Every character in this table must force
// quoting; the exact reproductions from the review are asserted separately
// in TestParseRender_CriticalMetacharacterRegressions.
func TestQuoteIfNeeded_MetacharactersForceQuoting(t *testing.T) {
	metachars := []byte(" \t\n\"'\\$`#&;|<>()")
	for _, ch := range metachars {
		s := "x" + string(ch) + "y"
		got := QuoteIfNeeded(s)
		if got == s {
			t.Fatalf("QuoteIfNeeded(%q) = %q, want it quoted (metachar %q left bare)", s, got, ch)
		}
		words, err := Words(got)
		if err != nil {
			t.Fatalf("Words(QuoteIfNeeded(%q)) = %q: unexpected error: %v", s, got, err)
		}
		if len(words) != 1 || words[0] != s {
			t.Fatalf("Words(QuoteIfNeeded(%q)) = %#v, want single word %q", s, words, s)
		}
	}
}

// TestQuoteIfNeeded_ExactOutputs pins the literal quoting choices so a
// mutation that swaps single/double quoting, or drops escaping in the
// double-quote fallback, is caught even though the round-trip would still
// (coincidentally) succeed for some of these inputs.
func TestQuoteIfNeeded_ExactOutputs(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"plain-id_123", "plain-id_123"},
		{"a b", "'a b'"},
		{"#tag", "'#tag'"},
		{"a&b", "'a&b'"},
		{"a;b", "'a;b'"},
		{"a(b)", "'a(b)'"},
		{"o'brien", `"o'brien"`},
		{`it's a "test" & $x`, `"it's a \"test\" & \$x"`},
	}
	for _, c := range cases {
		got := QuoteIfNeeded(c.in)
		if got != c.want {
			t.Fatalf("QuoteIfNeeded(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRender_QuotesArgs pins that Render quotes Args exactly like it quotes
// flag values. A mutation dropping QuoteIfNeeded(a) for Args (rendering the
// arg bare) would still produce a syntactically valid command, but re-parse
// it as extra positional words instead of the original single Arg.
func TestRender_QuotesArgs(t *testing.T) {
	c := &Command{Verb: "put", Args: []string{"a b", "#tag"}}
	got := c.Render()
	want := "put 'a b' '#tag'"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
	reparsed, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse(Render(c)) unexpected error: %v", err)
	}
	if !reflect.DeepEqual(reparsed.Args, c.Args) {
		t.Fatalf("round-trip Args = %#v, want %#v", reparsed.Args, c.Args)
	}
}

// TestRender_FlagsSortedDeterministically pins Render's sorted-flag-name
// output. A single Render() call could pass "by luck" even with unsorted
// map iteration (Go's randomization might happen to land on sorted order),
// so this repeats the call enough times that a mutation to unsorted
// iteration would show a differing result with overwhelming probability.
func TestRender_FlagsSortedDeterministically(t *testing.T) {
	c := &Command{
		Verb: "cfg",
		Flags: map[string]string{
			"zebra":  "1",
			"apple":  "2",
			"mango":  "3",
			"banana": "4",
			"yak":    "5",
		},
	}
	want := "cfg --apple=2 --banana=4 --mango=3 --yak=5 --zebra=1"
	for i := 0; i < 50; i++ {
		if got := c.Render(); got != want {
			t.Fatalf("Render() iteration %d = %q, want %q", i, got, want)
		}
	}
}

// TestParseRender_CriticalMetacharacterRegressions reproduces the exact
// inputs from the CRITICAL review finding verbatim, so a regression on any
// one of them fails loudly instead of silently corrupting a command.
func TestParseRender_CriticalMetacharacterRegressions(t *testing.T) {
	t.Run("comment metachar in Args", func(t *testing.T) {
		original := &Command{Verb: "put", Args: []string{"#tag"}}
		reparsed, err := Parse(original.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, original.Render())
		}
		if !reflect.DeepEqual(reparsed.Args, original.Args) {
			t.Fatalf("round-trip Args = %#v, want %#v (rendered: %s)", reparsed.Args, original.Args, original.Render())
		}
	})

	t.Run("empty word in Args", func(t *testing.T) {
		original := &Command{Verb: "put", Args: []string{""}}
		reparsed, err := Parse(original.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, original.Render())
		}
		if !reflect.DeepEqual(reparsed.Args, original.Args) {
			t.Fatalf("round-trip Args = %#v, want %#v (rendered: %s)", reparsed.Args, original.Args, original.Render())
		}
	})

	t.Run("ampersand in flag value", func(t *testing.T) {
		original := &Command{Verb: "put", Args: []string{"k"}, Flags: map[string]string{"value": "abc&"}}
		reparsed, err := Parse(original.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, original.Render())
		}
		if reparsed.Flags["value"] != "abc&" {
			t.Fatalf("round-trip Flags[value] = %q, want %q (rendered: %s)", reparsed.Flags["value"], "abc&", original.Render())
		}
	})

	t.Run("semicolon in flag value", func(t *testing.T) {
		original := &Command{Verb: "put", Args: []string{"k"}, Flags: map[string]string{"value": "abc;"}}
		reparsed, err := Parse(original.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, original.Render())
		}
		if reparsed.Flags["value"] != "abc;" {
			t.Fatalf("round-trip Flags[value] = %q, want %q (rendered: %s)", reparsed.Flags["value"], "abc;", original.Render())
		}
	})

	t.Run("parens in flag value", func(t *testing.T) {
		original := &Command{Verb: "put", Args: []string{"k"}, Flags: map[string]string{"value": "a(b)"}}
		reparsed, err := Parse(original.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, original.Render())
		}
		if reparsed.Flags["value"] != "a(b)" {
			t.Fatalf("round-trip Flags[value] = %q, want %q (rendered: %s)", reparsed.Flags["value"], "a(b)", original.Render())
		}
	})
}

// TestUnescapeDblQuotedLit targets unescapeDblQuotedLit directly. mvdan's
// syntax parser never resolves double-quote backslash escapes itself (see
// appendWordPart's doc comment), so this function carries that
// responsibility alone; each case below is one of the POSIX-meaningful
// escapes it must resolve, plus one that must NOT be touched.
func TestUnescapeDblQuotedLit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"escaped backslash", `a\\b`, `a\b`},
		{"escaped double quote", `a\"b`, `a"b`},
		{"escaped dollar", `a\$b`, `a$b`},
		{"escaped backtick", "a\\`b", "a`b"},
		{"line continuation dropped", "a\\\nb", "ab"},
		{"unrelated backslash left as-is", `a\nb`, `a\nb`},
		{"no backslash at all", "plain", "plain"},
	}
	for _, c := range cases {
		got := unescapeDblQuotedLit(c.in)
		if got != c.want {
			t.Fatalf("%s: unescapeDblQuotedLit(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestParseRender_RoundTrip 是本 Plan 的核心属性：Parse(Render(c)) == c。
// 生成输入而非固定表——手写表只会覆盖作者想到的情形，而引号/空格/JSON 恰恰是想不到的地方。
//
// verbs 始终是普通标识符：Render 从不对 Verb 调用 QuoteIfNeeded（Verb 是各类型
// <name>_command.go 里代码写死的 DSL 关键字，不是用户数据，天然不需要引号），所以
// 这一维故意不引入需要转义的取值——那测的是不存在的契约，不是遗漏的覆盖率。
// argSets / flagSets 则相反：这两维必须覆盖需要转义的取值（空格、空串、以及
// CRITICAL 发现里列出的每个 shell 元字符），否则组合爆炸出的用例其实都退化成
// 同一种"不需要引号"的行为，测不出 QuoteIfNeeded/Words 的引号处理是否正确。
func TestParseRender_RoundTrip(t *testing.T) {
	verbs := []string{"topic", "message", "get", "find"}
	argSets := [][]string{
		nil,
		{"create"},
		{"create", "orders"},
		{"produce", "orders-2024"},
		{"a b"},
		{"#x"},
		{""},
		{"tab\tnewline\nquote\"back`tick"},
		{"amp&semi;pipe|lt<gt>paren(paren)"},
	}
	flagSets := []map[string]string{
		nil,
		{"prefix": "true"},
		{"partitions": "3", "replication-factor": "2"},
		{"value": `{"a": 1, "b": "x y"}`},
		{"value": "hello world", "key": "k1"},
		{"filter": `{"name": "o'brien"}`},
		{"value": "abc&", "note": "a;b"},
		{"value": "a(b)"},
		{"value": ""},
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
