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

// TestParse_RejectsExactReviewRepros reproduces, verbatim, the five inputs
// from the round-2 review's IMPORTANT-1 finding: Render never quoted Verb or
// flag names, so Parse used to accept arbitrary strings there and mint a
// Command whose Render output either silently meant something different on
// re-parse, or flat-out failed to re-parse. The fix moves the check to
// Parse's boundary — these inputs must now be rejected up front, before a
// bad Command can even be constructed.
func TestParse_RejectsExactReviewRepros(t *testing.T) {
	cases := []string{
		`'a b' x`,       // unquoted Verb "a b" would re-tokenize as two words
		`get --'a b'=1`, // unquoted flag name "a b" would re-tokenize as flag "a" + arg "b=1"
		`'#x' delete`,   // unquoted Verb "#x" starts a shell comment on re-parse
		`'A=b' x`,       // unquoted Verb "A=b" re-parses as a variable assignment
		`'if' x`,        // unquoted Verb "if" re-parses as the start of an if-statement
	}
	for _, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) = nil error, want rejection", in)
		}
	}
}

// TestParse_RejectsUnsafeVerb pins validateVerb's character-allowlist half:
// any Verb outside safeVerbWord must be rejected, independent of the
// reserved-word check.
//
// Each case single-quotes the verb directly in the input string rather than
// going through Command.Render() — Render never quotes Verb (by design, see
// its doc comment), so rendering an already-unsafe Verb wouldn't reproduce
// the bug: it would just silently re-tokenize into multiple words on
// re-parse (exactly IMPORTANT-1's "SILENT" corruption case), never reaching
// validateVerb as the single bad token this test needs to hand it.
func TestParse_RejectsUnsafeVerb(t *testing.T) {
	cases := []string{"a b", "a&b", "a;b", "a#b", "A=b", "a$b", `a"b`}
	for _, verb := range cases {
		in := "'" + verb + "' x"
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) (Verb %q) = nil error, want rejection", in, verb)
		}
	}
}

// TestParse_RejectsShellReservedVerb pins validateVerb's reserved-word half.
// Every word here matches safeVerbWord's character allowlist (they're all
// plain letters), so without the dedicated reserved-word check they would
// pass straight through — and then break the round-trip on Render, since the
// shell grammar reads them as compound-command keywords in position 0, not
// literal command names.
func TestParse_RejectsShellReservedVerb(t *testing.T) {
	for word := range shellReservedWords {
		if _, err := Parse(word + " x"); err == nil {
			t.Fatalf("Parse(%q) = nil error, want rejection (reserved word %q as Verb)", word+" x", word)
		}
	}
}

// TestParse_RejectsEmptyVerb pins IMPORTANT-2: Parse of a command that is
// only a single quoted empty word used to mint &Command{Verb: ""} (a
// regression from Words no longer dropping deliberately-quoted empty
// words), and that Command's Render() ("") could not itself be re-parsed
// ("empty command"). validateVerb's safeVerbWord
// check rejects the empty string directly (the pattern requires at least
// one character), so this is covered by TestParse_RejectsUnsafeVerb's
// mechanism too — but the empty-Verb case gets its own dedicated test since
// it was the literal regression reported, not just an instance of the
// general character-allowlist rule.
func TestParse_RejectsEmptyVerb(t *testing.T) {
	if _, err := Parse(`''`); err == nil {
		t.Fatalf(`Parse("''") = nil error, want rejection`)
	}
}

// TestParse_RejectsUnsafeFlagName pins the flag-name half of IMPORTANT-1:
// a flag name built from an unquoted word containing shell metacharacters
// must be rejected the same way an unsafe Arg or Verb is, instead of being
// accepted and later rendered bare.
//
// As with TestParse_RejectsUnsafeVerb, each case single-quotes the flag name
// directly in the input string instead of going through Command.Render():
// Render never quotes flag names either, so an unsafe name would just
// silently re-tokenize into a different flag/arg split on re-parse (see the
// review's second SILENT repro, also covered verbatim by
// TestParse_RejectsExactReviewRepros).
func TestParse_RejectsUnsafeFlagName(t *testing.T) {
	cases := []string{"a b", "a&b", "a;b", "a#b"}
	for _, name := range cases {
		in := "get --'" + name + "'=1"
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) (flag name %q) = nil error, want rejection", in, name)
		}
	}
}

// TestQuoteIfNeeded_CRLFAndNULAreLossy pins MINOR-2's known, undocumented-
// until-now limitation: mvdan's parser normalizes CRLF to LF and drops NUL
// entirely, even for content inside single quotes, so no quoting choice
// QuoteIfNeeded could make avoids it. This is not reachable from model input
// today — Words normalizes CRLF at tokenization time, so a model-authored
// value can never carry a CR into a Command — but it pins the current
// behavior so a future change to either mvdan or this package's quoting is a
// deliberate decision, not a silent surprise.
func TestQuoteIfNeeded_CRLFAndNULAreLossy(t *testing.T) {
	t.Run("CRLF collapses to LF even single-quoted", func(t *testing.T) {
		c := &Command{Verb: "put", Args: []string{"k"}, Flags: map[string]string{"value": "a\r\nb"}}
		reparsed, err := Parse(c.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, c.Render())
		}
		if reparsed.Flags["value"] != "a\nb" {
			t.Fatalf("round-trip Flags[value] = %q, want %q (rendered: %s)", reparsed.Flags["value"], "a\nb", c.Render())
		}
	})

	t.Run("NUL is dropped even single-quoted", func(t *testing.T) {
		c := &Command{Verb: "put", Args: []string{"k"}, Flags: map[string]string{"value": "a\x00b"}}
		reparsed, err := Parse(c.Render())
		if err != nil {
			t.Fatalf("Parse(Render(c)) unexpected error: %v (rendered: %s)", err, c.Render())
		}
		if reparsed.Flags["value"] != "ab" {
			t.Fatalf("round-trip Flags[value] = %q, want %q (rendered: %s)", reparsed.Flags["value"], "ab", c.Render())
		}
	})
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
		// MINOR-3: no prior entry contains a literal backslash, so a mutation
		// that made the *syntax.SglQuoted case in appendWordPart run
		// unescapeDblQuotedLit (which is only correct inside double quotes —
		// single-quoted shell content never gets escape-processed) survived
		// the whole suite. This value renders single-quoted (no "'" inside
		// it, so QuoteIfNeeded doesn't fall back to the double-quote branch)
		// and carries two literal backslashes through that path.
		{"value": `{"re": "^a\\d+$"}`},
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
