// Package customtype 实现 customtype binder：设置 → 自定义类型的 Wails IPC 边界。
//
// 全部委托给 internal/service/custom_type_svc（不触碰仓储 / gorm，见
// internal/archtest 对 internal/app 的边界约束）。领域层的结构化校验错误
// （*custom_type_entity.ValidationError）与占用保护（*custom_type_svc.InUseError）
// 在这里转成前端可以直接逐行渲染的结果，而不是压扁成一条 error 字符串——Wails 只把
// error 序列化成 message，会丢掉 Issues / Assets。
package customtype

import (
	"context"
	"errors"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// CustomType binder。
type CustomType struct {
	ctx  context.Context
	lang LangProvider
}

// New 构造 customtype binder。
func New(lang LangProvider) *CustomType {
	return &CustomType{lang: lang}
}

// Startup 保存 Wails ctx。
func (c *CustomType) Startup(ctx context.Context) { c.ctx = ctx }

// Cleanup 无状态可清理。
func (c *CustomType) Cleanup() {}

func (c *CustomType) ctxWithLang() context.Context {
	return i18n.Ctx(c.ctx, c.lang.Lang())
}

// Summary 是类型列表一行：执行方式与在用资产数，供设置页列表展示（不含字段结构 /
// 模板绑定等编辑器才需要的细节）。
type Summary struct {
	ID         int64  `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	ExecMode   string `json:"execMode"`
	AssetCount int    `json:"assetCount"`
}

// ListCustomTypes 列出全部自定义类型，附带执行方式与在用资产数。
func (c *CustomType) ListCustomTypes() ([]Summary, error) {
	ctx := c.ctxWithLang()
	types, err := custom_type_svc.CustomType().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(types))
	for _, t := range types {
		n, err := custom_type_svc.CustomType().UsageCount(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Summary{
			ID: t.ID, Slug: t.Slug, Name: t.Name, Icon: t.Icon, ExecMode: t.ExecMode, AssetCount: n,
		})
	}
	return out, nil
}

// GetCustomType 读取单个类型的完整定义，供编辑器打开已有类型。
func (c *CustomType) GetCustomType(id int64) (*custom_type_entity.CustomType, error) {
	return custom_type_svc.CustomType().Get(c.ctxWithLang(), id)
}

// GetCustomTypeUsage 读取在用资产数，供编辑器底栏显示"已有 N 个资产使用"。
func (c *CustomType) GetCustomTypeUsage(id int64) (int, error) {
	return custom_type_svc.CustomType().UsageCount(c.ctxWithLang(), id)
}

// SaveResult 是保存的结构化结果。校验失败（含标识重名 / 被修改）时 Issues 非空、
// Type 为 nil，调用方按 Path 逐行标出原因；成功时 Type 是保存后的类型（含回填的
// ID / 默认策略），Warnings 是不阻止保存的提示（如命令模板引用了密钥字段）。
type SaveResult struct {
	Type     *custom_type_entity.CustomType `json:"type,omitempty"`
	Issues   []custom_type_entity.Issue     `json:"issues,omitempty"`
	Warnings []custom_type_entity.Issue     `json:"warnings,omitempty"`
}

// SaveCustomType 创建（ID == 0）或整体更新一个类型。
func (c *CustomType) SaveCustomType(ct *custom_type_entity.CustomType) (*SaveResult, error) {
	err := custom_type_svc.CustomType().Save(c.ctxWithLang(), ct)
	if err != nil {
		var verr *custom_type_entity.ValidationError
		if errors.As(err, &verr) {
			return &SaveResult{Issues: verr.Issues}, nil
		}
		return nil, err
	}
	return &SaveResult{Type: ct, Warnings: ct.Warnings()}, nil
}

// DeleteResult 是删除的结构化结果。还有资产在用时 Deleted 为 false、Assets 列出
// 占用资产的名称；没有资产在用时 Deleted 为 true。
type DeleteResult struct {
	Deleted bool     `json:"deleted"`
	Assets  []string `json:"assets,omitempty"`
}

// DeleteCustomType 删除一个类型；还有资产在用时通过 Assets 返回占用列表而不是报错。
func (c *CustomType) DeleteCustomType(id int64) (*DeleteResult, error) {
	err := custom_type_svc.CustomType().Delete(c.ctxWithLang(), id)
	if err != nil {
		var inUse *custom_type_svc.InUseError
		if errors.As(err, &inUse) {
			return &DeleteResult{Deleted: false, Assets: inUse.Assets}, nil
		}
		return nil, err
	}
	return &DeleteResult{Deleted: true}, nil
}
