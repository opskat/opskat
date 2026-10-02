// Package custom_type_entity 定义「自定义类型」：用户在设置里描述的一类服务
// （字段结构 + 执行方式 + 模板绑定 + 使用说明 + 默认策略）。通用资产
// （asset_entity.AssetTypeGeneric）必须基于一个自定义类型，资产上只存字段值
// （docs/specs/2026-09-28-generic-asset.md「自定义类型」，Design decision 2）。
package custom_type_entity

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/pkg/authtmpl"
)

// 执行方式，二选一（Design decision 3）。
const (
	ExecModeHTTP    = "http"
	ExecModeCommand = "command"
)

var (
	slugPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	identPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	httpDefaultAllow = []string{"GET *", "HEAD *", "OPTIONS *"}
)

// Field 是字段结构里的一行。字段只存数据，注入位置全部由模板表达。
type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
	// Default 在资产上没有该字段的值时生效；密钥字段不允许有默认值。
	Default string `json:"default,omitempty"`
}

// AuthBinding 是一条 HTTP 认证绑定：认证类型（authtmpl.AuthTypeFor 注册表）
// + 名称（仅 HasName 的类型）+ 值模板（个数等于 AuthType.ValueCount，
// basic 依次为用户名、密码）。
type AuthBinding struct {
	Type   string   `json:"type"`
	Name   string   `json:"name,omitempty"`
	Values []string `json:"values"`
}

// HTTPConfig 是 HTTP 执行方式的配置。BaseURL 是模板，渲染结果必须是 http/https
// 绝对 URL —— 该检查依赖字段值，由执行器在渲染后做。
type HTTPConfig struct {
	BaseURL string        `json:"base_url"`
	Auth    []AuthBinding `json:"auth,omitempty"`
}

// EnvBinding 是本地命令方式的一条环境变量绑定：变量名 = 值模板。
type EnvBinding struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CommandConfig 是本地命令执行方式的配置。Template 可以为空：为空时 exec 传整条
// shell 命令，不为空时 exec 只传参数（Design decision 6）。
type CommandConfig struct {
	Template string       `json:"template"`
	Env      []EnvBinding `json:"env,omitempty"`
}

// CustomType 自定义类型实体。Slug 是对外标识（opsctl --type、put_asset type、
// help、DocGate 键），也是通用资产引用类型的键，创建后不可修改（Design decision 13）。
// HTTP / Command 只有与 ExecMode 对应的那一个有意义。
type CustomType struct {
	ID       int64          `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Slug     string         `gorm:"column:slug;type:varchar(64);not null;uniqueIndex" json:"slug"`
	Name     string         `gorm:"column:name;type:varchar(255);not null" json:"name"`
	Icon     string         `gorm:"column:icon;type:varchar(100)" json:"icon"`
	ExecMode string         `gorm:"column:exec_mode;type:varchar(20);not null" json:"execMode"`
	Fields   []Field        `gorm:"column:fields;type:text;serializer:json" json:"fields"`
	HTTP     *HTTPConfig    `gorm:"column:http_config;type:text;serializer:json" json:"http,omitempty"`
	Command  *CommandConfig `gorm:"column:command_config;type:text;serializer:json" json:"command,omitempty"`
	// Usage 是 Markdown 使用说明（这类服务怎么用），进入 help。
	Usage string `gorm:"column:usage;type:text" json:"usage"`
	// DefaultPolicy 是类型上的默认策略，新建资产时复制一份（Design decision 10）。
	DefaultPolicy *policy.CommandPolicy `gorm:"column:default_policy;type:text;serializer:json" json:"defaultPolicy"`
	Createtime    int64                 `gorm:"column:createtime" json:"createtime"`
	Updatetime    int64                 `gorm:"column:updatetime" json:"updatetime"`
}

// TableName GORM 表名
func (CustomType) TableName() string {
	return "custom_types"
}

// Issue 是一条校验问题。Path 指向出错的行，供类型编辑器在对应行标出原因，
// 取值形如 "name"、"slug"、"exec_mode"、"fields"、"fields[2].name"、
// "fields[1].default"、"http"、"http.base_url"、"http.auth[0].type"、
// "http.auth[0].name"、"http.auth[0].values"、"http.auth[0].values[1]"、
// "command"、"command.template"、"command.env[1].name"、"command.env[1].value"、
// "format"（导入文件）。Code 是下面的问题码之一（模板问题为
// IssueTemplatePrefix + authtmpl 错误码），Params 是填进文案的参数——界面按
// Code 翻译成界面语言（frontend 的 customType.issue.<code>），不在后端拼文案。
type Issue struct {
	Path   string            `json:"path"`
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// 问题码。值是与前端语言包之间的契约，改名要同步两份语言包。
const (
	IssueNameRequired             = "name_required"
	IssueSlugInvalid              = "slug_invalid"
	IssueSlugImmutable            = "slug_immutable"
	IssueSlugReserved             = "slug_reserved"
	IssueSlugTaken                = "slug_taken"
	IssueExecModeInvalid          = "exec_mode_invalid"
	IssueFieldsRequired           = "fields_required"
	IssueFieldNameInvalid         = "field_name_invalid"
	IssueFieldNameDuplicate       = "field_name_duplicate"
	IssueSecretDefault            = "secret_default_not_allowed"
	IssueHTTPConfigMissing        = "http_config_missing"
	IssueBaseURLRequired          = "base_url_required"
	IssueAuthTypeUnknown          = "auth_type_unknown"
	IssueAuthNameRequired         = "auth_name_required"
	IssueAuthNameNotAllowed       = "auth_name_not_allowed"
	IssueAuthValueCount           = "auth_value_count"
	IssueAuthValueRequired        = "auth_value_required"
	IssueCommandConfigMissing     = "command_config_missing"
	IssueEnvNameInvalid           = "env_name_invalid"
	IssueEnvNameDuplicate         = "env_name_duplicate"
	IssueCommandSecretInArgs      = "command_secret_in_args"
	IssueImportFormatUnknown      = "format_unknown"
	IssueImportVersionUnsupported = "version_unsupported"
	// IssueTemplatePrefix 加在 authtmpl.ParseError.Code 前面，Params 沿用 ParseError 的。
	IssueTemplatePrefix = "template."
)

// NewIssue 构造一条问题；kv 是成对的参数名 / 参数值。
func NewIssue(path, code string, kv ...string) Issue {
	is := Issue{Path: path, Code: code}
	if len(kv) > 0 {
		is.Params = make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			is.Params[kv[i]] = kv[i+1]
		}
	}
	return is
}

// ValidationError 汇总一次校验发现的全部问题（而不是遇到第一个就停）。
type ValidationError struct {
	Issues []Issue `json:"issues"`
}

// Error 给日志与包装报错用：逐条列出路径、问题码与参数（界面不用它，按 Issues 翻译）。
func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, is := range e.Issues {
		part := is.Path + ": " + is.Code
		if len(is.Params) > 0 {
			keys := make([]string, 0, len(is.Params))
			for k := range is.Params {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			kvs := make([]string, 0, len(keys))
			for _, k := range keys {
				kvs = append(kvs, k+"="+strconv.Quote(is.Params[k]))
			}
			part += " (" + strings.Join(kvs, ", ") + ")"
		}
		parts = append(parts, part)
	}
	return "invalid custom type: " + strings.Join(parts, "; ")
}

type issues []Issue

func (l *issues) add(path, code string, kv ...string) {
	*l = append(*l, NewIssue(path, code, kv...))
}

func (l issues) err() error {
	if len(l) == 0 {
		return nil
	}
	return &ValidationError{Issues: l}
}

// FieldNames 返回字段名列表，即模板可以引用的字段。
func (c *CustomType) FieldNames() []string {
	names := make([]string, 0, len(c.Fields))
	for _, f := range c.Fields {
		names = append(names, f.Name)
	}
	return names
}

// FieldByName 按字段名查找字段定义。
func (c *CustomType) FieldByName(name string) (Field, bool) {
	for _, f := range c.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Validate 做不依赖其他数据的保存校验：基本信息、字段结构、执行方式与绑定模板
// （经 authtmpl.Parse：语法、未知字段 / 函数、request.* 只在 HTTP 认证里可用）。
// 标识是否与内置 / 扩展 / 其他自定义类型重名、标识是否被修改，由服务层校验。
// 有问题时返回 *ValidationError。
func (c *CustomType) Validate() error {
	var l issues
	if strings.TrimSpace(c.Name) == "" {
		l.add("name", IssueNameRequired)
	}
	if !slugPattern.MatchString(c.Slug) {
		l.add("slug", IssueSlugInvalid, "slug", c.Slug)
	}
	c.validateFields(&l)
	fields := c.FieldNames()
	switch c.ExecMode {
	case ExecModeHTTP:
		c.validateHTTP(&l, fields)
	case ExecModeCommand:
		c.validateCommand(&l, fields)
	default:
		l.add("exec_mode", IssueExecModeInvalid, "mode", c.ExecMode)
	}
	return l.err()
}

func (c *CustomType) validateFields(l *issues) {
	if len(c.Fields) == 0 {
		l.add("fields", IssueFieldsRequired)
		return
	}
	seen := make(map[string]bool, len(c.Fields))
	for i, f := range c.Fields {
		path := fmt.Sprintf("fields[%d]", i)
		switch {
		case !identPattern.MatchString(f.Name):
			l.add(path+".name", IssueFieldNameInvalid, "name", f.Name)
		case seen[f.Name]:
			l.add(path+".name", IssueFieldNameDuplicate, "name", f.Name)
		}
		seen[f.Name] = true
		if f.Secret && f.Default != "" {
			l.add(path+".default", IssueSecretDefault)
		}
	}
}

func (c *CustomType) validateHTTP(l *issues, fields []string) {
	if c.HTTP == nil {
		l.add("http", IssueHTTPConfigMissing)
		return
	}
	if strings.TrimSpace(c.HTTP.BaseURL) == "" {
		l.add("http.base_url", IssueBaseURLRequired)
	} else {
		parseInto(l, "http.base_url", c.HTTP.BaseURL, fields, false)
	}
	for i, b := range c.HTTP.Auth {
		path := fmt.Sprintf("http.auth[%d]", i)
		at, ok := authtmpl.AuthTypeFor(b.Type)
		if !ok {
			l.add(path+".type", IssueAuthTypeUnknown, "type", b.Type)
			continue
		}
		if at.HasName && strings.TrimSpace(b.Name) == "" {
			l.add(path+".name", IssueAuthNameRequired, "type", b.Type)
		}
		if !at.HasName && b.Name != "" {
			l.add(path+".name", IssueAuthNameNotAllowed, "type", b.Type)
		}
		if len(b.Values) != at.ValueCount {
			l.add(path+".values", IssueAuthValueCount,
				"type", b.Type, "want", strconv.Itoa(at.ValueCount), "got", strconv.Itoa(len(b.Values)))
		}
		for j, v := range b.Values {
			valuePath := fmt.Sprintf("%s.values[%d]", path, j)
			// header/query value and basic username must not be empty; basic password can be empty
			if b.Type != "basic" || j == 0 {
				if strings.TrimSpace(v) == "" {
					l.add(valuePath, IssueAuthValueRequired)
					continue
				}
			}
			parseInto(l, valuePath, v, fields, true)
		}
	}
}

func (c *CustomType) validateCommand(l *issues, fields []string) {
	if c.Command == nil {
		l.add("command", IssueCommandConfigMissing)
		return
	}
	parseInto(l, "command.template", c.Command.Template, fields, false)
	seen := make(map[string]bool, len(c.Command.Env))
	for i, e := range c.Command.Env {
		path := fmt.Sprintf("command.env[%d]", i)
		switch {
		case !identPattern.MatchString(e.Name):
			l.add(path+".name", IssueEnvNameInvalid, "name", e.Name)
		case seen[e.Name]:
			l.add(path+".name", IssueEnvNameDuplicate, "name", e.Name)
		}
		seen[e.Name] = true
		parseInto(l, path+".value", e.Value, fields, false)
	}
}

func parseInto(l *issues, path, src string, fields []string, allowRequest bool) {
	_, err := authtmpl.Parse(src, authtmpl.ParseOptions{Fields: fields, AllowRequest: allowRequest})
	var perr *authtmpl.ParseError
	if errors.As(err, &perr) {
		*l = append(*l, Issue{Path: path, Code: IssueTemplatePrefix + perr.Code, Params: perr.Params})
	}
}

// Warnings 返回不阻止保存的提示：命令模板引用了密钥字段时，命令行参数会被本机
// 其他进程（ps）看到。调用方应在类型通过 Validate 后再展示这些提示。
func (c *CustomType) Warnings() []Issue {
	if c.ExecMode != ExecModeCommand || c.Command == nil || c.Command.Template == "" {
		return nil
	}
	// authtmpl 只在 Parse 时报告引用的字段：只允许非密钥字段时解析失败、允许全部
	// 字段时解析成功，说明模板引用了密钥字段。
	var plain []string
	for _, f := range c.Fields {
		if !f.Secret {
			plain = append(plain, f.Name)
		}
	}
	if _, err := authtmpl.Parse(c.Command.Template, authtmpl.ParseOptions{Fields: c.FieldNames()}); err != nil {
		return nil
	}
	if _, err := authtmpl.Parse(c.Command.Template, authtmpl.ParseOptions{Fields: plain}); err == nil {
		return nil
	}
	return []Issue{NewIssue("command.template", IssueCommandSecretInArgs)}
}

// DefaultPolicyFor 返回新建类型时按执行方式预填的默认规则（Design decision 9）：
// HTTP 放行 GET / HEAD / OPTIONS，命令方式没有默认放行。
func DefaultPolicyFor(execMode string) *policy.CommandPolicy {
	if execMode == ExecModeHTTP {
		return &policy.CommandPolicy{AllowList: append([]string(nil), httpDefaultAllow...)}
	}
	return &policy.CommandPolicy{}
}
