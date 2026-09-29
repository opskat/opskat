package customtype

import (
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// secretMask 在渲染详情页的实际地址时顶替密钥字段的值：地址只用于展示，Base URL 模板
// 若引用了密钥，展示结果里也不能出现明文。
const secretMask = "****"

// GenericFieldView 是通用资产详情页的一个字段。密钥字段从不携带明文：只报告是否已设置、
// 是否托管凭据及凭据名；查看明文走 RevealGenericSecret。
type GenericFieldView struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	// Value 是非密钥字段的值（未填写时为默认值）；密钥字段恒为空。
	Value string `json:"value,omitempty"`
	// Set 表示字段有值（密钥：直接输入的密文或托管凭据引用）。
	Set            bool   `json:"set"`
	CredentialID   int64  `json:"credentialId,omitempty"`
	CredentialName string `json:"credentialName,omitempty"`
	// Missing 表示必填但为空（类型新增了必填字段、这台实例还没填）。
	Missing bool `json:"missing"`
}

// GenericAssetView 是通用资产详情页所需的解析结果：所属类型、各字段值（不含密钥明文）、
// 缺值的必填字段与渲染后的实际地址（HTTP 方式）。
type GenericAssetView struct {
	TypeID   int64              `json:"typeId"`
	Slug     string             `json:"slug"`
	TypeName string             `json:"typeName"`
	TypeIcon string             `json:"typeIcon"`
	ExecMode string             `json:"execMode"`
	Usage    string             `json:"usage"`
	Fields   []GenericFieldView `json:"fields"`
	Missing  []string           `json:"missing"`
	// ActualAddress 是 HTTP 方式渲染后的 Base URL（密钥位置以掩码代替）；渲染失败时为空。
	ActualAddress string `json:"actualAddress,omitempty"`
}

// GetGenericAssetView 解析一台通用资产供详情页展示。
func (c *CustomType) GetGenericAssetView(assetID int64) (*GenericAssetView, error) {
	ctx := c.ctxWithLang()
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, err
	}
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return nil, err
	}
	resolved, err := custom_type_svc.CustomType().ResolveAsset(ctx, asset)
	if err != nil {
		return nil, err
	}
	ct := resolved.Type
	missing := make(map[string]bool, len(resolved.Missing))
	for _, name := range resolved.Missing {
		missing[name] = true
	}

	view := &GenericAssetView{
		TypeID:   ct.ID,
		Slug:     ct.Slug,
		TypeName: ct.Name,
		TypeIcon: ct.Icon,
		ExecMode: ct.ExecMode,
		Usage:    ct.Usage,
		Fields:   make([]GenericFieldView, 0, len(ct.Fields)),
		Missing:  append([]string{}, resolved.Missing...),
	}
	// 渲染地址用的字段值：密钥换成掩码，保证展示结果里不出现明文。
	display := make(map[string]string, len(resolved.Values))
	for _, f := range ct.Fields {
		value := resolved.Values[f.Name]
		fv := GenericFieldView{
			Name:     f.Name,
			Label:    f.Label,
			Secret:   f.Secret,
			Required: f.Required,
			Set:      value != "",
			Missing:  missing[f.Name],
		}
		display[f.Name] = value
		if f.Secret {
			if value != "" {
				display[f.Name] = secretMask
			}
			if stored := cfg.Values[f.Name]; stored.CredentialID > 0 {
				cred, err := credential_mgr_svc.Get(ctx, stored.CredentialID)
				if err != nil {
					return nil, fmt.Errorf("字段 %s 引用的托管凭据 %d: %w", f.Name, stored.CredentialID, err)
				}
				fv.CredentialID = cred.ID
				fv.CredentialName = cred.Name
			}
		} else {
			fv.Value = value
		}
		view.Fields = append(view.Fields, fv)
	}

	if ct.ExecMode == custom_type_entity.ExecModeHTTP && ct.HTTP != nil {
		u, err := helper.RenderGenericBaseURL(ct, display, time.Now())
		if err != nil {
			// exec 会用同一个渲染把错误原样报出来；详情页只是不显示地址这一行。
			logger.Ctx(ctx).Warn("render generic base url for detail view", zap.Int64("assetID", assetID), zap.Error(err))
		} else {
			view.ActualAddress = u.Redacted()
		}
	}
	return view, nil
}

// RevealGenericSecret 返回通用资产一个密钥字段的明文，供详情页 / 表单点眼睛查看
// （与 System.GetAssetPassword 同一类显式查看入口）。非密钥字段不走这里。
func (c *CustomType) RevealGenericSecret(assetID int64, field string) (string, error) {
	ctx := c.ctxWithLang()
	logger.Ctx(ctx).Info("reveal generic secret start", zap.Int64("assetID", assetID), zap.String("field", field))
	plain, err := revealGenericSecret(c, assetID, field)
	if err != nil {
		logger.Ctx(ctx).Error("reveal generic secret failed", zap.Int64("assetID", assetID), zap.String("field", field), zap.Error(err))
		return "", err
	}
	logger.Ctx(ctx).Info("reveal generic secret done", zap.Int64("assetID", assetID), zap.String("field", field))
	return plain, nil
}

func revealGenericSecret(c *CustomType, assetID int64, field string) (string, error) {
	ctx := c.ctxWithLang()
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", err
	}
	if !asset.IsGeneric() {
		return "", fmt.Errorf("资产 %q 不是通用资产", asset.Name)
	}
	resolved, err := custom_type_svc.CustomType().ResolveAsset(ctx, asset)
	if err != nil {
		return "", err
	}
	f, ok := resolved.Type.FieldByName(field)
	if !ok {
		return "", fmt.Errorf("自定义类型 %q 没有字段 %s", resolved.Type.Slug, field)
	}
	if !f.Secret {
		return "", fmt.Errorf("字段 %s 不是密钥字段", field)
	}
	return resolved.Values[field], nil
}
