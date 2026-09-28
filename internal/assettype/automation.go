package assettype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/pkg/jsonscalar"
)

// AutomationContract is owned by one registered asset type. It declares the
// complete generic create surface and the non-secret subset safe for approval.
type AutomationContract struct {
	ConfigFields   []string
	ApprovalFields []string
	// FlatMapFields lists ApprovalFields whose value, when a flat string→string map (no
	// nested secret can hide inside a bare string), may pass through SafeApprovalDetail /
	// SafeAuditArgs un-redacted (e.g. Redis node_address_map). Any other approval field
	// that happens to hold a map value (composite/attacker-supplied) is still dropped by
	// approvalView's default scalar-only rule — this allowlist is opt-in per field, not a
	// blanket exception for "looks flat".
	FlatMapFields []string
	Normalize     func(map[string]any) error
	// ValidateCreate / Validate are the contract-level counterparts of the handler's
	// ValidateCreateArgs (create only) and ValidateAutomationConfig (create and update),
	// for contracts resolved per asset whose rules the handler cannot know statically.
	ValidateCreate func(map[string]any) error
	Validate       func(map[string]any) error
	CredentialPlan func(map[string]any) (CredentialPlan, error)
	BindCredential func(map[string]any, CredentialBinding) (map[string]any, error)
}

type CredentialKind string

const (
	CredentialKindNone      CredentialKind = "none"
	CredentialKindReference CredentialKind = "reference"
)

// CredentialPlan is pure data for later existing-reference validation. Secret
// values remain write-only and never appear in PreparedCreate.Approval.
type CredentialPlan struct {
	Kind          CredentialKind
	ReferenceID   int64
	AcceptedTypes []string
}

type CredentialBinding struct {
	ID   int64
	Type string
}

type automationNormalizer interface {
	NormalizeAutomationConfig(map[string]any) error
}

type automationValidator interface {
	ValidateAutomationConfig(map[string]any) error
}

// dynamicAutomationContract is implemented by a handler whose automation surface depends
// on the asset being written (the generic handler: the fields come from the asset's
// custom type). Its static AutomationContract() is empty; every write resolves the
// contract through this hook instead.
type dynamicAutomationContract interface {
	AutomationContractFor(ctx context.Context, a *asset_entity.Asset) (AutomationContract, error)
}

// typeNameOwner is implemented by a handler that serves caller-facing type names other
// than its own Type() (the generic handler serves every custom-type slug).
type typeNameOwner interface {
	// NewAssetForTypeName reports whether name is one of the handler's type names and, if
	// so, returns a new asset stamped with that type identity.
	NewAssetForTypeName(ctx context.Context, name string) (*asset_entity.Asset, bool, error)
	// TypeNameOf returns the caller-facing type name of one of the handler's assets.
	TypeNameOf(a *asset_entity.Asset) string
}

// ErrUnknownType reports a type name that no registered handler serves.
var ErrUnknownType = errors.New("unsupported asset type")

// NewAsset returns a new, unsaved asset for a caller-facing type name (opsctl --type,
// put_asset type): a registered type name maps to itself, any other name is offered to
// the handlers that serve additional names (a custom-type slug becomes a generic asset
// bound to that type). A name nobody serves wraps ErrUnknownType.
func NewAsset(ctx context.Context, typeName string) (*asset_entity.Asset, error) {
	if _, ok := Get(typeName); ok {
		return &asset_entity.Asset{Type: typeName}, nil
	}
	for _, h := range All() {
		owner, ok := h.(typeNameOwner)
		if !ok {
			continue
		}
		a, owned, err := owner.NewAssetForTypeName(ctx, typeName)
		if err != nil {
			return nil, err
		}
		if owned {
			return a, nil
		}
	}
	return nil, fmt.Errorf("%w %q (registered types: %s, or a custom type slug)", ErrUnknownType, typeName, joinRegisteredTypes())
}

// TypeName returns the caller-facing type name of an asset — the inverse of NewAsset:
// the custom-type slug for a generic asset, the stored type otherwise.
func TypeName(a *asset_entity.Asset) string {
	if h, ok := Get(a.Type); ok {
		if owner, ok := h.(typeNameOwner); ok {
			return owner.TypeNameOf(a)
		}
	}
	return a.Type
}

// ContractOf returns the automation contract that governs writes to a: the handler's
// static contract, or the one its dynamic hook resolves for this asset.
func ContractOf(ctx context.Context, a *asset_entity.Asset) (AssetTypeHandler, AutomationContract, error) {
	h, ok := Get(a.Type)
	if !ok {
		return nil, AutomationContract{}, fmt.Errorf("%w %q (registered types: %s)", ErrUnknownType, a.Type, joinRegisteredTypes())
	}
	dynamic, ok := h.(dynamicAutomationContract)
	if !ok {
		return h, h.AutomationContract(), nil
	}
	contract, err := dynamic.AutomationContractFor(ctx, a)
	if err != nil {
		return nil, AutomationContract{}, err
	}
	return h, contract, nil
}

type PreparedCreate struct {
	Handler    AssetTypeHandler
	Config     map[string]any
	Approval   map[string]any
	Credential CredentialPlan
	contract   AutomationContract
}

// BindCredential applies a validated existing credential through the selected type owner.
func (p PreparedCreate) BindCredential(binding CredentialBinding) (map[string]any, error) {
	if binding.ID <= 0 {
		return nil, fmt.Errorf("credential binding ID must be positive")
	}
	bind := p.contract.BindCredential
	if bind == nil {
		return nil, fmt.Errorf("managed credentials are not applicable to asset type %q", p.Handler.Type())
	}
	return bind(p.Config, binding)
}

func newAutomationContract(configFields, approvalFields []string, normalize func(map[string]any) error, plan func(map[string]any) (CredentialPlan, error), bind func(map[string]any, CredentialBinding) (map[string]any, error)) AutomationContract {
	return AutomationContract{
		ConfigFields:   sortedUnique(configFields),
		ApprovalFields: sortedUnique(approvalFields),
		Normalize:      normalize,
		CredentialPlan: plan,
		BindCredential: bind,
	}
}

// PrepareCreate validates a create of the unsaved asset a (see NewAsset) through the
// contract its type owns.
func PrepareCreate(ctx context.Context, a *asset_entity.Asset, args map[string]any) (PreparedCreate, error) {
	prepared, err := prepareAutomation(ctx, a, args)
	if err != nil {
		return PreparedCreate{}, err
	}
	if prepared.contract.Normalize != nil {
		if err := prepared.contract.Normalize(prepared.Config); err != nil {
			return PreparedCreate{}, err
		}
	}
	if err := normalizeAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	if err := prepared.Handler.ValidateCreateArgs(prepared.Config); err != nil {
		return PreparedCreate{}, err
	}
	if prepared.contract.ValidateCreate != nil {
		if err := prepared.contract.ValidateCreate(prepared.Config); err != nil {
			return PreparedCreate{}, err
		}
	}
	if err := validateAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	return finalizeAutomation(prepared)
}

// PrepareUpdate validates a partial update of the existing asset a through the same
// type-owned field and credential declarations without applying create-only defaults or
// required fields.
func PrepareUpdate(ctx context.Context, a *asset_entity.Asset, args map[string]any) (PreparedCreate, error) {
	prepared, err := prepareAutomation(ctx, a, args)
	if err != nil {
		return PreparedCreate{}, err
	}
	if err := normalizeAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	if err := validateAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	return finalizeAutomation(prepared)
}

func normalizeAutomation(prepared PreparedCreate) error {
	if normalizer, ok := prepared.Handler.(automationNormalizer); ok {
		return normalizer.NormalizeAutomationConfig(prepared.Config)
	}
	return nil
}

func validateAutomation(prepared PreparedCreate) error {
	if validator, ok := prepared.Handler.(automationValidator); ok {
		if err := validator.ValidateAutomationConfig(prepared.Config); err != nil {
			return err
		}
	}
	if prepared.contract.Validate != nil {
		return prepared.contract.Validate(prepared.Config)
	}
	return nil
}

func finalizeAutomation(prepared PreparedCreate) (PreparedCreate, error) {
	contract := prepared.contract
	prepared.Approval = approvalView(prepared.Config, contract.ApprovalFields, contract.FlatMapFields)
	credential, err := credentialPlan(contract, prepared.Config)
	if err != nil {
		return PreparedCreate{}, err
	}
	prepared.Credential = credential
	return prepared, nil
}

func prepareAutomation(ctx context.Context, a *asset_entity.Asset, args map[string]any) (PreparedCreate, error) {
	h, contract, err := ContractOf(ctx, a)
	if err != nil {
		return PreparedCreate{}, err
	}
	if len(contract.ConfigFields) == 0 {
		return PreparedCreate{}, fmt.Errorf("asset type %q has no automation config contract", a.Type)
	}
	config := cloneArgs(args)
	if err := rejectUnknownFields(config, contract.ConfigFields); err != nil {
		return PreparedCreate{}, fmt.Errorf("invalid %s config: %w", TypeName(a), err)
	}
	return PreparedCreate{Handler: h, Config: config, contract: contract}, nil
}

func credentialPlan(contract AutomationContract, config map[string]any) (CredentialPlan, error) {
	if contract.CredentialPlan == nil {
		return CredentialPlan{Kind: CredentialKindNone}, nil
	}
	return contract.CredentialPlan(config)
}

func RegisteredTypes() []string {
	handlers := All()
	out := make([]string, 0, len(handlers))
	for _, h := range handlers {
		out = append(out, h.Type())
	}
	return out
}

func rejectUnknownFields(args map[string]any, accepted []string) error {
	allowed := make(map[string]struct{}, len(accepted))
	for _, field := range accepted {
		allowed[field] = struct{}{}
	}
	unknown := make([]string, 0)
	for field := range args {
		if _, ok := allowed[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown config field(s): %v", unknown)
}

func approvalView(args map[string]any, fields []string, flatMapFields []string) map[string]any {
	mapAllowed := make(map[string]struct{}, len(flatMapFields))
	for _, field := range flatMapFields {
		mapAllowed[field] = struct{}{}
	}
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		value, ok := args[field]
		if !ok {
			continue
		}
		if strings, ok := copyFlatStringArray(value); ok {
			out[field] = strings
			continue
		}
		// map 值只对显式加入 FlatMapFields 的字段放行(如 Redis node_address_map)；其它
		// 字段哪怕值恰好是扁平字符串 map(如攻击者构造的 auth_type={"password":secret})，
		// 也要按下面的标量规则整体省略，不能靠"形状是扁平的"就当作安全。
		if _, allowed := mapAllowed[field]; allowed {
			if m := ArgStringMap(args, field); m != nil {
				out[field] = m
				continue
			}
		}
		// 只拷贝能安全 JSON 编码的标量（nil/bool/string/有限数值，含命名标量别名与合法
		// json.Number）。复合值（map/slice/array/struct/pointer）整体省略——嵌套 secret
		// 不能借任何允许审批字段进入 SafeApprovalDetail / SafeAuditArgs。
		if jsonscalar.IsScalar(value) {
			out[field] = value
		}
	}
	return out
}

// copyFlatStringArray 把扁平字符串数组（[]string 或 []any 且每一项都是 string）拷贝成新的
// []string，使审批视图既不与 config 共享可变切片（无 mutation alias），也不放行藏了嵌套值的
// 复合项。[]any 含任一非字符串项（嵌套 map/数字/布尔/切片）就不是扁平字符串数组，整体拒绝。
func copyFlatStringArray(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...), true
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func cloneArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for key, value := range args {
		switch v := value.(type) {
		case []string:
			out[key] = append([]string(nil), v...)
		case []any:
			out[key] = append([]any(nil), v...)
		default:
			out[key] = value
		}
	}
	return out
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func joinRegisteredTypes() string {
	return fmt.Sprintf("%v", RegisteredTypes())
}

func passwordReferencePlan(args map[string]any, plaintextField string) (CredentialPlan, error) {
	credentialID, _, err := positiveInt64Arg(args, "credential_id")
	if err != nil {
		return CredentialPlan{}, err
	}
	plaintext := ArgString(args, plaintextField)
	if credentialID > 0 && plaintext != "" {
		return CredentialPlan{}, fmt.Errorf("credential_id and %s are mutually exclusive", plaintextField)
	}
	if credentialID > 0 {
		return CredentialPlan{
			Kind:          CredentialKindReference,
			ReferenceID:   credentialID,
			AcceptedTypes: []string{credential_entity.TypePassword},
		}, nil
	}
	if plaintext == "" {
		return CredentialPlan{Kind: CredentialKindNone}, nil
	}
	return CredentialPlan{Kind: CredentialKindNone}, nil
}

func bindPasswordCredential(args map[string]any, binding CredentialBinding) (map[string]any, error) {
	if binding.Type != credential_entity.TypePassword {
		return nil, fmt.Errorf("credential type %q is not accepted; expected password", binding.Type)
	}
	out := cloneArgs(args)
	out["credential_id"] = binding.ID
	return out, nil
}

func noCredentialPlan(fields ...string) func(map[string]any) (CredentialPlan, error) {
	return func(args map[string]any) (CredentialPlan, error) {
		for _, field := range fields {
			if value, ok := args[field]; ok && valuePresent(value) {
				return CredentialPlan{}, fmt.Errorf("%s is not applicable to this asset configuration", field)
			}
		}
		return CredentialPlan{Kind: CredentialKindNone}, nil
	}
}

func positiveInt64Arg(args map[string]any, key string) (int64, bool, error) {
	value, supplied := args[key]
	if !supplied {
		return 0, false, nil
	}
	var id int64
	switch typed := value.(type) {
	case int:
		id = int64(typed)
	case int64:
		id = typed
	case float64:
		id = int64(typed)
		if float64(id) != typed {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
	case json.Number:
		// opsctl 按 UseNumber 解码 --config，嵌套对象里的数字（通用资产密钥字段的
		// {"credential_id": N}）以 json.Number 到达。
		parsed, err := typed.Int64()
		if err != nil {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = parsed
	default:
		return 0, true, fmt.Errorf("%s must be a positive integer", key)
	}
	if id <= 0 {
		return 0, true, fmt.Errorf("%s must be a positive integer", key)
	}
	return id, true, nil
}

func valuePresent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return true
	}
}
