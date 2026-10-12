package authtmpl_test

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/opskat/opskat/internal/pkg/authtmpl"
)

// 决定 19：这套语言是扩展 auth 绑定既有语法的超集。下面几条是扩展语法里有、
// 自定义类型最初的语法里没有的写法。

func render(t *testing.T, tpl *authtmpl.Template, fields map[string]string) string {
	t.Helper()
	got, err := tpl.Render(authtmpl.NewRenderContext(fields))
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	return got
}

// base64 接受多个参数，先按顺序拼接再编码，等同于对 `+` 拼接的结果编码。
func TestParseAndRender_Base64JoinsItsArguments(t *testing.T) {
	opts := authtmpl.ParseOptions{Fields: []string{"apiKeyId", "apiKey"}}
	fields := map[string]string{"apiKeyId": "id-1", "apiKey": "s3cret"}

	got := render(t, mustParse(t, `ApiKey {{base64(apiKeyId, ":", apiKey)}}`, opts), fields)
	if want := "ApiKey " + base64.StdEncoding.EncodeToString([]byte("id-1:s3cret")); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
	if concat := render(t, mustParse(t, `ApiKey {{base64(apiKeyId + ":" + apiKey)}}`, opts), fields); got != concat {
		t.Fatalf("base64(a, b, c) = %q, base64(a + b + c) = %q, want them equal", got, concat)
	}
}

// 多参数只属于 base64：其余函数的参数个数仍然固定。
func TestParse_OnlyBase64IsVariadic(t *testing.T) {
	_, err := authtmpl.Parse(`{{hex(a, b)}}`, authtmpl.ParseOptions{Fields: []string{"a", "b"}})
	var perr *authtmpl.ParseError
	if !errors.As(err, &perr) || perr.Code != authtmpl.CodeWrongArgCount {
		t.Fatalf("Parse error = %v, want code %q", err, authtmpl.CodeWrongArgCount)
	}
}

// 标识符可以含 `-`：扩展 configSchema 的属性名允许这样写。
func TestParseAndRender_IdentifierMayContainHyphen(t *testing.T) {
	tpl := mustParse(t, `{{key-id}}/{{base64(key-id, ":", api-key)}}`, authtmpl.ParseOptions{Fields: []string{"key-id", "api-key"}})
	got := render(t, tpl, map[string]string{"key-id": "k", "api-key": "v"})
	if want := "k/" + base64.StdEncoding.EncodeToString([]byte("k:v")); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

// `-` 只是标识符的一部分，不是运算符，也不能开头。
func TestParse_HyphenIsNotAnOperator(t *testing.T) {
	_, err := authtmpl.Parse(`{{a -b}}`, authtmpl.ParseOptions{Fields: []string{"a", "b"}})
	var perr *authtmpl.ParseError
	if !errors.As(err, &perr) || perr.Code != authtmpl.CodeUnexpectedCharacter {
		t.Fatalf("Parse error = %v, want code %q", err, authtmpl.CodeUnexpectedCharacter)
	}
}

// 字符串字面量里的 `}}` 不结束表达式。
func TestParseAndRender_ClosingBracesInsideStringLiteral(t *testing.T) {
	tpl := mustParse(t, `[{{base64("}}", token)}}] {{"a\"}}b"}} tail`, authtmpl.ParseOptions{Fields: []string{"token"}})
	got := render(t, tpl, map[string]string{"token": "x"})
	if want := "[" + base64.StdEncoding.EncodeToString([]byte("}}x")) + `] a"}}b tail`; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

// AnyField 把字段校验交给调用方：任何标识符都按字段解析，Fields() 报出模板
// 引用到的字段（按首次出现的顺序、不重复），不含函数名与 now.* / request.*。
func TestParse_AnyFieldLeavesFieldCheckToTheCaller(t *testing.T) {
	tpl := mustParse(t, `{{b}} {{base64(a, ":", b)}} {{hex(hmac_sha256(c, now.unix + a))}} {{uuid()}}`,
		authtmpl.ParseOptions{AnyField: true})
	if got, want := tpl.Fields(), []string{"b", "a", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %v, want %v", got, want)
	}
}

func TestTemplate_FieldsOfATemplateWithoutReferences(t *testing.T) {
	tpl := mustParse(t, `Bearer {{"fixed" + now.unix}}`, authtmpl.ParseOptions{})
	if got := tpl.Fields(); len(got) != 0 {
		t.Fatalf("Fields() = %v, want none", got)
	}
}

// AnyField 只放开字段名：未知函数、request.* 的限制照旧。
func TestParse_AnyFieldKeepsTheOtherChecks(t *testing.T) {
	cases := map[string]string{
		`{{md5(a)}}`:         authtmpl.CodeUnknownFunction,
		`{{request.method}}`: authtmpl.CodeRequestNotAllowed,
		`{{env.home}}`:       authtmpl.CodeUnknownReference,
	}
	for src, code := range cases {
		_, err := authtmpl.Parse(src, authtmpl.ParseOptions{AnyField: true})
		var perr *authtmpl.ParseError
		if !errors.As(err, &perr) || perr.Code != code {
			t.Errorf("Parse(%q) error = %v, want code %q", src, err, code)
		}
	}
}

// 字符串字面量的转义与 Go 的双引号字符串字面量相同：扩展原先用 strconv.Unquote
// 解码字面量，已发布的模板渲染出的字节不能变。
func TestParseAndRender_StringLiteralTakesGoEscapes(t *testing.T) {
	cases := map[string]string{
		`"a\tb\rc"`:           "a\tb\rc",
		`"\a\b\f\v"`:          "\a\b\f\v",
		`"\x41\x00\xff"`:      "A\x00\xff",
		`"\101\000\377"`:      "A\x00\xff",
		`"\u00e9\u4e2d"`:      "é中",
		`"\U0001F600"`:        "😀",
		`"é\n\"\\ \t}}\x7d"`:  "é\n\"\\ \t}}}",
		`"\\x41 \\t \\u00e9"`: `\x41 \t \u00e9`,
	}
	for lit, want := range cases {
		unquoted, err := strconv.Unquote(lit)
		if err != nil || unquoted != want {
			t.Fatalf("test case %s: strconv.Unquote = %q, %v, want %q", lit, unquoted, err, want)
		}
		if got := render(t, mustParse(t, "{{"+lit+"}}", authtmpl.ParseOptions{}), nil); got != want {
			t.Errorf("Render({{%s}}) = %q, want %q", lit, got, want)
		}
		joined := render(t, mustParse(t, "{{base64("+lit+`, "|")}}`, authtmpl.ParseOptions{}), nil)
		if wantJoined := base64.StdEncoding.EncodeToString([]byte(want + "|")); joined != wantJoined {
			t.Errorf("Render({{base64(%s, \"|\")}}) = %q, want %q", lit, joined, wantJoined)
		}
	}
}

// Go 字符串字面量不接受的转义仍是 invalid_escape，char 是反斜杠后面的那个字符。
func TestParse_EscapesGoRejectsAreInvalid(t *testing.T) {
	cases := map[string]string{
		`"\q"`:         "q",
		`"\'"`:         "'",
		`"\8"`:         "8",
		`"\x4"`:        "x",
		`"\xzz"`:       "x",
		`"\400"`:       "4",
		`"\12"`:        "1",
		`"\u12"`:       "u",
		`"\ud800"`:     "u",
		`"\U00110000"`: "U",
	}
	for lit, char := range cases {
		if _, err := strconv.Unquote(lit); err == nil {
			t.Fatalf("test case %s: strconv.Unquote accepts it", lit)
		}
		_, err := authtmpl.Parse("{{"+lit+"}}", authtmpl.ParseOptions{})
		var perr *authtmpl.ParseError
		if !errors.As(err, &perr) || perr.Code != authtmpl.CodeInvalidEscape || perr.Params["char"] != char {
			t.Errorf("Parse({{%s}}) error = %v, want code %q with char %q", lit, err, authtmpl.CodeInvalidEscape, char)
		}
	}
}
