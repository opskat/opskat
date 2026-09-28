package assettype

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// genericHandler 是通用资产的处理器。通用资产必须基于一个自定义类型：调用方以类型
// 标识（slug）作为资产类型（opsctl --type <slug> / put_asset type=<slug>），存储时
// Type 为 generic、GenericConfig.CustomType 为该标识。字段契约、默认策略都来自该类型，
// 因此经 dynamicAutomationContract / typeNameOwner / 按资产的默认策略提供者按资产解析，
// 而不是静态声明。
type genericHandler struct{}

func init() {
	Register(&genericHandler{})
	policy.RegisterAssetDefaultPolicy(asset_entity.AssetTypeGeneric, genericDefaultPolicy)
}

var errGenericNeedsCustomType = errors.New("a generic asset is created through its custom type: " +
	"pass the custom type slug as the asset type (opsctl --type <slug>, put_asset type=<slug>)")

func (h *genericHandler) Type() string     { return asset_entity.AssetTypeGeneric }
func (h *genericHandler) DefaultPort() int { return 0 }

// SafeView 只给出自定义类型标识：脱离类型结构分不清哪些值是密钥（密文与明文都是
// 字符串），字段值由 help 按类型结构展示。
func (h *genericHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetGenericConfig()
	if err != nil {
		return nil
	}
	return map[string]any{"custom_type": cfg.CustomType}
}

// ResolvePassword 通用资产的密钥按字段存放，没有单一密码。
func (h *genericHandler) ResolvePassword(_ context.Context, a *asset_entity.Asset) (string, error) {
	return "", fmt.Errorf("generic asset %q has no single password; its secrets are stored per field", a.Name)
}

// DefaultPolicy 仅为满足接口：新建资产的默认策略按资产取自其自定义类型（genericDefaultPolicy）。
func (h *genericHandler) DefaultPolicy() any { return &asset_entity.CommandPolicy{} }
func (h *genericHandler) PolicyKind() string { return policy.PolicyKindCommand }

// AutomationContract 为空：字段契约由 AutomationContractFor 按资产的自定义类型解析。
func (h *genericHandler) AutomationContract() AutomationContract { return AutomationContract{} }

// AutomationContractFor 由资产的自定义类型生成字段契约：键是字段名，非密钥字段进入
// 审批，密钥字段只写。
func (h *genericHandler) AutomationContractFor(ctx context.Context, a *asset_entity.Asset) (AutomationContract, error) {
	ct, err := genericCustomType(ctx, a)
	if err != nil {
		return AutomationContract{}, err
	}
	var approval []string
	for _, f := range ct.Fields {
		if !f.Secret {
			approval = append(approval, f.Name)
		}
	}
	return AutomationContract{
		ConfigFields:   sortedUnique(ct.FieldNames()),
		ApprovalFields: sortedUnique(approval),
		ValidateCreate: func(args map[string]any) error { return requireGenericFields(ct, args) },
		Validate:       func(args map[string]any) error { return validateGenericValues(ctx, ct, args) },
	}, nil
}

func (h *genericHandler) NewAssetForTypeName(ctx context.Context, name string) (*asset_entity.Asset, bool, error) {
	ct, err := custom_type_svc.CustomType().GetBySlug(ctx, name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeGeneric}
	if err := a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: ct.Slug}); err != nil {
		return nil, false, err
	}
	return a, true, nil
}

func (h *genericHandler) TypeNameOf(a *asset_entity.Asset) string {
	cfg, err := a.GetGenericConfig()
	if err != nil || cfg.CustomType == "" {
		return a.Type
	}
	return cfg.CustomType
}

// ValidateCreateArgs 必填校验依赖自定义类型，由 AutomationContractFor 的 ValidateCreate 负责。
func (h *genericHandler) ValidateCreateArgs(_ map[string]any) error { return nil }

func (h *genericHandler) ApplyCreateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error {
	ct, err := genericCustomType(ctx, a)
	if err != nil {
		return err
	}
	cfg := &asset_entity.GenericConfig{CustomType: ct.Slug, Values: map[string]asset_entity.GenericValue{}}
	if err := storeGenericValues(ctx, ct, cfg, args); err != nil {
		return err
	}
	return a.SetGenericConfig(cfg)
}

// ApplyUpdateArgs 只改写给出的字段，其余字段的值保持不变。
func (h *genericHandler) ApplyUpdateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error {
	ct, err := genericCustomType(ctx, a)
	if err != nil {
		return err
	}
	cfg, err := a.GetGenericConfig()
	if err != nil {
		return err
	}
	if cfg.Values == nil {
		cfg.Values = map[string]asset_entity.GenericValue{}
	}
	if err := storeGenericValues(ctx, ct, cfg, args); err != nil {
		return err
	}
	return a.SetGenericConfig(cfg)
}

// genericDefaultPolicy 是通用资产的按资产默认策略：复制其自定义类型上的默认规则
// （Design decision 10），之后按资产各自管理。
func genericDefaultPolicy(ctx context.Context, assetConfig string) (any, error) {
	ct, err := genericCustomType(ctx, &asset_entity.Asset{Type: asset_entity.AssetTypeGeneric, Config: assetConfig})
	if err != nil {
		return nil, err
	}
	return ct.DefaultPolicy, nil
}

func genericCustomType(ctx context.Context, a *asset_entity.Asset) (*custom_type_entity.CustomType, error) {
	if a.Config == "" {
		return nil, errGenericNeedsCustomType
	}
	cfg, err := a.GetGenericConfig()
	if err != nil {
		return nil, err
	}
	if cfg.CustomType == "" {
		return nil, errGenericNeedsCustomType
	}
	return custom_type_svc.CustomType().GetBySlug(ctx, cfg.CustomType)
}

// genericInput 是 config 里一个字段的值：普通字段只能是字符串；密钥字段是明文字符串，
// 或 {"credential_id": N} 引用托管密码凭据。
type genericInput struct {
	plain        string
	credentialID int64
}

func (in genericInput) empty() bool { return in.plain == "" && in.credentialID == 0 }

func parseGenericInput(f custom_type_entity.Field, raw any) (genericInput, error) {
	if s, ok := raw.(string); ok {
		return genericInput{plain: s}, nil
	}
	if !f.Secret {
		return genericInput{}, fmt.Errorf("field %s must be a string", f.Name)
	}
	shapeErr := fmt.Errorf(`secret field %s must be a plaintext string or {"credential_id": N}`, f.Name)
	ref, ok := raw.(map[string]any)
	if !ok || len(ref) != 1 {
		return genericInput{}, shapeErr
	}
	id, supplied, err := positiveInt64Arg(ref, "credential_id")
	if !supplied {
		return genericInput{}, shapeErr
	}
	if err != nil {
		return genericInput{}, fmt.Errorf("secret field %s: %w", f.Name, err)
	}
	return genericInput{credentialID: id}, nil
}

// requireGenericFields 是创建时的必填校验：没有给出且没有默认值的必填字段。
func requireGenericFields(ct *custom_type_entity.CustomType, args map[string]any) error {
	var missing []string
	for _, f := range ct.Fields {
		if _, given := args[f.Name]; !given && f.Required && f.Default == "" {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid %s config: missing required field(s): %s", ct.Slug, strings.Join(missing, ", "))
	}
	return nil
}

// validateGenericValues 校验给出的每个字段值的形状、必填字段不得为空，以及引用的托管
// 凭据存在且是密码凭据。创建与更新都经过这里，一次报告全部问题。
func validateGenericValues(ctx context.Context, ct *custom_type_entity.CustomType, args map[string]any) error {
	var problems []string
	for _, f := range ct.Fields {
		raw, given := args[f.Name]
		if !given {
			continue
		}
		in, err := parseGenericInput(f, raw)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if f.Required && in.empty() {
			problems = append(problems, fmt.Sprintf("required field %s must not be empty", f.Name))
		}
		if in.credentialID > 0 {
			if err := requirePasswordCredential(ctx, in.credentialID); err != nil {
				problems = append(problems, fmt.Sprintf("secret field %s: %v", f.Name, err))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid %s config: %s", ct.Slug, strings.Join(problems, "; "))
	}
	return nil
}

// storeGenericValues 把给出的字段值按存储约定写进 cfg：密钥明文加密保存，托管凭据
// 只存引用（在写入事务里再确认一次仍是密码凭据），普通字段存明文。
func storeGenericValues(ctx context.Context, ct *custom_type_entity.CustomType, cfg *asset_entity.GenericConfig, args map[string]any) error {
	for _, f := range ct.Fields {
		raw, given := args[f.Name]
		if !given {
			continue
		}
		in, err := parseGenericInput(f, raw)
		if err != nil {
			return err
		}
		switch {
		case in.credentialID > 0:
			if err := requirePasswordCredential(ctx, in.credentialID); err != nil {
				return fmt.Errorf("secret field %s: %w", f.Name, err)
			}
			cfg.Values[f.Name] = asset_entity.GenericValue{CredentialID: in.credentialID}
		case f.Secret && in.plain != "":
			cipher, err := credential_svc.Default().Encrypt(in.plain)
			if err != nil {
				return fmt.Errorf("encrypt secret field %s: %w", f.Name, err)
			}
			cfg.Values[f.Name] = asset_entity.GenericValue{Value: cipher}
		default:
			cfg.Values[f.Name] = asset_entity.GenericValue{Value: in.plain}
		}
	}
	return nil
}

func requirePasswordCredential(ctx context.Context, id int64) error {
	_, err := credential_mgr_svc.RequireType(ctx, id, []string{credential_entity.TypePassword})
	return err
}
