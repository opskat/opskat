package custom_type_entity

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpType() *CustomType {
	return &CustomType{
		Name:     "Grafana",
		Slug:     "grafana",
		ExecMode: ExecModeHTTP,
		Fields: []Field{
			{Name: "host", Label: "Host", Required: true},
			{Name: "token", Label: "Token", Secret: true, Required: true},
		},
		HTTP: &HTTPConfig{
			BaseURL: "https://{{host}}",
			Auth: []AuthBinding{
				{Type: "header", Name: "Authorization", Values: []string{`{{"Bearer " + token}}`}},
			},
		},
	}
}

func commandType() *CustomType {
	return &CustomType{
		Name:     "AWS",
		Slug:     "aws",
		ExecMode: ExecModeCommand,
		Fields: []Field{
			{Name: "region", Label: "Region", Default: "us-east-1"},
			{Name: "access_key", Secret: true, Required: true},
		},
		Command: &CommandConfig{
			Template: "aws --region {{region}} --output json",
			Env:      []EnvBinding{{Name: "AWS_ACCESS_KEY_ID", Value: "{{access_key}}"}},
		},
	}
}

// issuePaths 从 Validate 的错误里取出所有问题路径，断言校验标出了哪一行。
func issuePaths(t *testing.T, err error) []string {
	t.Helper()
	var verr *ValidationError
	require.True(t, errors.As(err, &verr), "expected *ValidationError, got %v", err)
	paths := make([]string, 0, len(verr.Issues))
	for _, is := range verr.Issues {
		paths = append(paths, is.Path)
	}
	return paths
}

func TestValidate_AcceptsValidTypes(t *testing.T) {
	assert.NoError(t, httpType().Validate())
	assert.NoError(t, commandType().Validate())

	empty := commandType()
	empty.Command.Template = "" // 命令模板可以为空（Design decision 6）
	assert.NoError(t, empty.Validate())

	basic := httpType()
	basic.HTTP.Auth = []AuthBinding{{Type: "basic", Values: []string{"{{host}}", "{{token}}"}}}
	assert.NoError(t, basic.Validate())

	signed := httpType()
	signed.HTTP.Auth = []AuthBinding{{Type: "header", Name: "X-Sign",
		Values: []string{"{{hex(hmac_sha256(token, request.method + request.path))}}"}}}
	assert.NoError(t, signed.Validate(), "request.* 在 HTTP 认证里可用")
}

func TestValidate_BasicInfo(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CustomType)
		path   string
	}{
		{"name required", func(c *CustomType) { c.Name = "  " }, "name"},
		{"slug too short", func(c *CustomType) { c.Slug = "a" }, "slug"},
		{"slug uppercase", func(c *CustomType) { c.Slug = "Grafana" }, "slug"},
		{"slug leading digit", func(c *CustomType) { c.Slug = "1abc" }, "slug"},
		{"slug too long", func(c *CustomType) { c.Slug = "a234567890123456789012345678901234" }, "slug"},
		{"unknown exec mode", func(c *CustomType) { c.ExecMode = "store" }, "exec_mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := httpType()
			tc.mutate(ct)
			assert.Contains(t, issuePaths(t, ct.Validate()), tc.path)
		})
	}
	ok := httpType()
	ok.Slug = "a1-b"
	assert.NoError(t, ok.Validate())
}

func TestValidate_Fields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CustomType)
		path   string
	}{
		{"at least one field", func(c *CustomType) { c.Fields = nil; c.HTTP.BaseURL = "https://x"; c.HTTP.Auth = nil }, "fields"},
		{"invalid field name", func(c *CustomType) { c.Fields[0].Name = "1host" }, "fields[0].name"},
		{"field name with dash", func(c *CustomType) { c.Fields[0].Name = "my-host" }, "fields[0].name"},
		{"duplicate field name", func(c *CustomType) { c.Fields[1].Name = "host" }, "fields[1].name"},
		{"secret with default", func(c *CustomType) { c.Fields[1].Default = "x" }, "fields[1].default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := httpType()
			tc.mutate(ct)
			assert.Contains(t, issuePaths(t, ct.Validate()), tc.path)
		})
	}
}

func TestValidate_HTTP(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CustomType)
		path   string
	}{
		{"missing http config", func(c *CustomType) { c.HTTP = nil }, "http"},
		{"base url required", func(c *CustomType) { c.HTTP.BaseURL = "" }, "http.base_url"},
		{"base url syntax error", func(c *CustomType) { c.HTTP.BaseURL = "https://{{host" }, "http.base_url"},
		{"base url unknown field", func(c *CustomType) { c.HTTP.BaseURL = "https://{{hostname}}" }, "http.base_url"},
		{"base url request.* not allowed", func(c *CustomType) { c.HTTP.BaseURL = "https://{{request.path}}" }, "http.base_url"},
		{"unknown auth type", func(c *CustomType) { c.HTTP.Auth[0].Type = "sigv4" }, "http.auth[0].type"},
		{"header needs name", func(c *CustomType) { c.HTTP.Auth[0].Name = "" }, "http.auth[0].name"},
		{"basic has no name", func(c *CustomType) {
			c.HTTP.Auth[0] = AuthBinding{Type: "basic", Name: "x", Values: []string{"{{host}}", "{{token}}"}}
		}, "http.auth[0].name"},
		{"basic needs two values", func(c *CustomType) {
			c.HTTP.Auth[0] = AuthBinding{Type: "basic", Values: []string{"{{host}}"}}
		}, "http.auth[0].values"},
		{"auth value unknown function", func(c *CustomType) { c.HTTP.Auth[0].Values[0] = "{{md5(token)}}" }, "http.auth[0].values[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := httpType()
			tc.mutate(ct)
			assert.Contains(t, issuePaths(t, ct.Validate()), tc.path)
		})
	}
}

func TestValidate_Command(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CustomType)
		path   string
	}{
		{"missing command config", func(c *CustomType) { c.Command = nil }, "command"},
		{"template unknown field", func(c *CustomType) { c.Command.Template = "aws {{profile}}" }, "command.template"},
		{"template request.* not allowed", func(c *CustomType) { c.Command.Template = "x {{request.method}}" }, "command.template"},
		{"env invalid name", func(c *CustomType) { c.Command.Env[0].Name = "AWS-KEY" }, "command.env[0].name"},
		{"env duplicate name", func(c *CustomType) {
			c.Command.Env = append(c.Command.Env, EnvBinding{Name: "AWS_ACCESS_KEY_ID", Value: "x"})
		}, "command.env[1].name"},
		{"env value syntax error", func(c *CustomType) { c.Command.Env[0].Value = "{{access_key" }, "command.env[0].value"},
		{"env value request.* not allowed", func(c *CustomType) { c.Command.Env[0].Value = "{{request.body}}" }, "command.env[0].value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := commandType()
			tc.mutate(ct)
			assert.Contains(t, issuePaths(t, ct.Validate()), tc.path)
		})
	}
}

func TestValidate_ReportsEveryIssue(t *testing.T) {
	ct := httpType()
	ct.Name = ""
	ct.Fields[1].Name = "host"
	ct.HTTP.Auth[0].Type = "nope"
	paths := issuePaths(t, ct.Validate())
	assert.Subset(t, paths, []string{"name", "fields[1].name", "http.auth[0].type"})
}

func TestWarnings_SecretInCommandTemplate(t *testing.T) {
	ct := commandType()
	assert.Empty(t, ct.Warnings(), "密钥只进环境变量时不提示")

	ct.Command.Template = "aws --key {{access_key}}"
	warnings := ct.Warnings()
	require.Len(t, warnings, 1)
	assert.Equal(t, "command.template", warnings[0].Path)

	h := httpType()
	assert.Empty(t, h.Warnings(), "HTTP 方式不涉及命令行参数")
}

// 界面按 Code 翻译、用 Params 填文案（跟随界面语言），所以问题只携带错误码与参数，
// 模板问题沿用 authtmpl 的错误码（加 template. 前缀），不带任何 "authtmpl:" 字样。
func TestValidate_IssuesCarryCodeAndParams(t *testing.T) {
	cases := []struct {
		name   string
		ct     func() *CustomType
		mutate func(*CustomType)
		want   Issue
	}{
		{"name required", httpType, func(c *CustomType) { c.Name = "" },
			Issue{Path: "name", Code: "name_required"}},
		{"slug invalid", httpType, func(c *CustomType) { c.Slug = "A" },
			Issue{Path: "slug", Code: "slug_invalid", Params: map[string]string{"slug": "A"}}},
		{"exec mode invalid", httpType, func(c *CustomType) { c.ExecMode = "store" },
			Issue{Path: "exec_mode", Code: "exec_mode_invalid", Params: map[string]string{"mode": "store"}}},
		{"fields required", httpType, func(c *CustomType) { c.Fields = nil; c.HTTP.BaseURL = "https://x"; c.HTTP.Auth = nil },
			Issue{Path: "fields", Code: "fields_required"}},
		{"field name invalid", httpType, func(c *CustomType) { c.Fields[0].Name = "my-host" },
			Issue{Path: "fields[0].name", Code: "field_name_invalid", Params: map[string]string{"name": "my-host"}}},
		{"field name duplicate", httpType, func(c *CustomType) { c.Fields[1].Name = "host" },
			Issue{Path: "fields[1].name", Code: "field_name_duplicate", Params: map[string]string{"name": "host"}}},
		{"secret default", httpType, func(c *CustomType) { c.Fields[1].Default = "x" },
			Issue{Path: "fields[1].default", Code: "secret_default_not_allowed"}},
		{"http config missing", httpType, func(c *CustomType) { c.HTTP = nil },
			Issue{Path: "http", Code: "http_config_missing"}},
		{"base url required", httpType, func(c *CustomType) { c.HTTP.BaseURL = " " },
			Issue{Path: "http.base_url", Code: "base_url_required"}},
		{"base url template", httpType, func(c *CustomType) { c.HTTP.BaseURL = "https://{{hostname}}" },
			Issue{Path: "http.base_url", Code: "template.unknown_field", Params: map[string]string{"name": "hostname", "expr": "hostname"}}},
		{"auth type unknown", httpType, func(c *CustomType) { c.HTTP.Auth[0].Type = "sigv4" },
			Issue{Path: "http.auth[0].type", Code: "auth_type_unknown", Params: map[string]string{"type": "sigv4"}}},
		{"auth name required", httpType, func(c *CustomType) { c.HTTP.Auth[0].Name = "" },
			Issue{Path: "http.auth[0].name", Code: "auth_name_required", Params: map[string]string{"type": "header"}}},
		{"auth name not allowed", httpType, func(c *CustomType) {
			c.HTTP.Auth[0] = AuthBinding{Type: "basic", Name: "x", Values: []string{"{{host}}", "{{token}}"}}
		}, Issue{Path: "http.auth[0].name", Code: "auth_name_not_allowed", Params: map[string]string{"type": "basic"}}},
		{"auth value count", httpType, func(c *CustomType) { c.HTTP.Auth[0] = AuthBinding{Type: "basic", Values: []string{"{{host}}"}} },
			Issue{Path: "http.auth[0].values", Code: "auth_value_count", Params: map[string]string{"type": "basic", "want": "2", "got": "1"}}},
		{"auth value template", httpType, func(c *CustomType) { c.HTTP.Auth[0].Values[0] = "{{md5(token)}}" },
			Issue{Path: "http.auth[0].values[0]", Code: "template.unknown_function", Params: map[string]string{"name": "md5", "expr": "md5(token)"}}},
		{"command config missing", commandType, func(c *CustomType) { c.Command = nil },
			Issue{Path: "command", Code: "command_config_missing"}},
		{"command template", commandType, func(c *CustomType) { c.Command.Template = "x {{request.method}}" },
			Issue{Path: "command.template", Code: "template.request_not_allowed", Params: map[string]string{"expr": "request.method"}}},
		{"env name invalid", commandType, func(c *CustomType) { c.Command.Env[0].Name = "AWS-KEY" },
			Issue{Path: "command.env[0].name", Code: "env_name_invalid", Params: map[string]string{"name": "AWS-KEY"}}},
		{"env name duplicate", commandType, func(c *CustomType) {
			c.Command.Env = append(c.Command.Env, EnvBinding{Name: "AWS_ACCESS_KEY_ID", Value: "x"})
		}, Issue{Path: "command.env[1].name", Code: "env_name_duplicate", Params: map[string]string{"name": "AWS_ACCESS_KEY_ID"}}},
		{"env value template", commandType, func(c *CustomType) { c.Command.Env[0].Value = "{{access_key" },
			Issue{Path: "command.env[0].value", Code: "template.unterminated_expression", Params: map[string]string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := tc.ct()
			tc.mutate(ct)
			var verr *ValidationError
			require.ErrorAs(t, ct.Validate(), &verr)
			assert.Contains(t, verr.Issues, tc.want)
		})
	}
}

func TestWarnings_CarryCode(t *testing.T) {
	ct := commandType()
	ct.Command.Template = "aws --key {{access_key}}"
	assert.Equal(t, []Issue{{Path: "command.template", Code: "command_secret_in_args"}}, ct.Warnings())
}

func TestDefaultPolicyFor(t *testing.T) {
	p := DefaultPolicyFor(ExecModeHTTP)
	assert.Equal(t, []string{"GET *", "HEAD *", "OPTIONS *"}, p.AllowList)
	assert.Empty(t, p.DenyList)

	c := DefaultPolicyFor(ExecModeCommand)
	assert.True(t, c.IsEmpty(), "命令方式没有默认放行")
}

func TestFieldByName(t *testing.T) {
	ct := httpType()
	f, ok := ct.FieldByName("token")
	require.True(t, ok)
	assert.True(t, f.Secret)
	_, ok = ct.FieldByName("nope")
	assert.False(t, ok)
}
