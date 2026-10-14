package authtmpl

import "fmt"

// Parse 静态校验错误的错误码。类型编辑器与导入预览按错误码翻译成界面语言
// （frontend 的 customType.issue.template.<code>），所以这些值是对外契约，改名要
// 同步两份语言包。
const (
	CodeUnterminatedExpression = "unterminated_expression"
	CodeEmptyExpression        = "empty_expression"
	CodeUnexpectedToken        = "unexpected_token"
	CodeUnexpectedCharacter    = "unexpected_character"
	CodeUnterminatedString     = "unterminated_string"
	CodeInvalidEscape          = "invalid_escape"
	CodeExpectedIdentifier     = "expected_identifier"
	CodeUnknownField           = "unknown_field"
	CodeUnknownReference       = "unknown_reference"
	CodeRequestNotAllowed      = "request_not_allowed"
	CodeMissingCloseParen      = "missing_close_paren"
	CodeUnknownFunction        = "unknown_function"
	CodeWrongArgCount          = "wrong_arg_count"
)

// ParseError 是 Parse 的静态校验错误。Code 是上面的错误码之一，Params 是填进界面
// 文案的参数（出错的表达式在 "expr"，不含两侧的花括号）；Error() 是给日志 / AI /
// opsctl 看的英文描述。
type ParseError struct {
	Code   string
	Params map[string]string
	msg    string
}

func (e *ParseError) Error() string {
	if expr, ok := e.Params["expr"]; ok {
		return fmt.Sprintf("authtmpl: %s (in {{%s}})", e.msg, expr)
	}
	return "authtmpl: " + e.msg
}

// newParseError 构造一个 *ParseError；kv 是成对的参数名 / 参数值。
func newParseError(code, msg string, kv ...string) *ParseError {
	params := make(map[string]string, len(kv)/2+1)
	for i := 0; i+1 < len(kv); i += 2 {
		params[kv[i]] = kv[i+1]
	}
	return &ParseError{Code: code, Params: params, msg: msg}
}
