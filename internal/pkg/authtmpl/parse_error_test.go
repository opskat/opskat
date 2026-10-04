package authtmpl_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/pkg/authtmpl"
)

// 类型编辑器与导入预览按 Code 翻译、用 Params 填文案（跟随界面语言），所以 Parse 的
// 静态校验错误必须是带稳定错误码的 *ParseError，而不是一段英文字符串。
func TestParse_ErrorCarriesCodeAndParams(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		opts   authtmpl.ParseOptions
		code   string
		params map[string]string
	}{
		{"unterminated", `Bearer {{token`, authtmpl.ParseOptions{Fields: []string{"token"}}, "unterminated_expression", map[string]string{}},
		{"empty", `{{ }}`, authtmpl.ParseOptions{}, "empty_expression", map[string]string{"expr": ""}},
		{"unknown field", `{{ nope }}`, authtmpl.ParseOptions{Fields: []string{"token"}}, "unknown_field", map[string]string{"expr": "nope", "name": "nope"}},
		{"unknown function", `{{md5(token)}}`, authtmpl.ParseOptions{Fields: []string{"token"}}, "unknown_function", map[string]string{"expr": "md5(token)", "name": "md5"}},
		{"wrong arity", `{{base64()}}`, authtmpl.ParseOptions{}, "wrong_arg_count", map[string]string{"expr": "base64()", "name": "base64", "want": "1", "got": "0"}},
		{"missing paren", `{{base64("a"}}`, authtmpl.ParseOptions{}, "missing_close_paren", map[string]string{"expr": `base64("a"`, "name": "base64"}},
		{"request not allowed", `{{request.method}}`, authtmpl.ParseOptions{}, "request_not_allowed", map[string]string{"expr": "request.method"}},
		{"unknown now attr", `{{now.tomorrow}}`, authtmpl.ParseOptions{}, "unknown_reference", map[string]string{"expr": "now.tomorrow", "ref": "now.tomorrow"}},
		{"unknown request attr", `{{request.query}}`, authtmpl.ParseOptions{AllowRequest: true}, "unknown_reference", map[string]string{"expr": "request.query", "ref": "request.query"}},
		{"unknown root", `{{env.home}}`, authtmpl.ParseOptions{}, "unknown_reference", map[string]string{"expr": "env.home", "ref": "env.home"}},
		{"dangling dot", `{{now.}}`, authtmpl.ParseOptions{}, "expected_identifier", map[string]string{"expr": "now.", "after": "now."}},
		{"trailing token", `{{"a" "b"}}`, authtmpl.ParseOptions{}, "unexpected_token", map[string]string{"expr": `"a" "b"`}},
		{"leading plus", `{{+ "a"}}`, authtmpl.ParseOptions{}, "unexpected_token", map[string]string{"expr": `+ "a"`}},
		{"bad character", `{{a * b}}`, authtmpl.ParseOptions{Fields: []string{"a", "b"}}, "unexpected_character", map[string]string{"expr": "a * b", "char": "*"}},
		{"bad escape", `{{"a\qb"}}`, authtmpl.ParseOptions{}, "invalid_escape", map[string]string{"expr": `"a\qb"`, "char": "q"}},
		{"unterminated string", `{{"abc}}`, authtmpl.ParseOptions{}, "unterminated_string", map[string]string{"expr": `"abc`}},
		{"unterminated escape", `{{"abc\}}`, authtmpl.ParseOptions{}, "unterminated_string", map[string]string{"expr": `"abc\`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := authtmpl.Parse(tc.src, tc.opts)
			var perr *authtmpl.ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("Parse(%q) error = %v, want *authtmpl.ParseError", tc.src, err)
			}
			if perr.Code != tc.code {
				t.Errorf("Code = %q, want %q", perr.Code, tc.code)
			}
			if !reflect.DeepEqual(perr.Params, tc.params) {
				t.Errorf("Params = %v, want %v", perr.Params, tc.params)
			}
		})
	}
}

// Error() 仍是给日志 / AI / opsctl 看的英文描述，指明出错的表达式。
func TestParseError_ErrorNamesTheExpression(t *testing.T) {
	_, err := authtmpl.Parse(`https://{{hostname}}`, authtmpl.ParseOptions{Fields: []string{"host"}})
	if err == nil || !strings.Contains(err.Error(), `unknown field "hostname"`) || !strings.Contains(err.Error(), "{{hostname}}") {
		t.Fatalf("Error() = %v, want it to name the unknown field and the expression", err)
	}
}
