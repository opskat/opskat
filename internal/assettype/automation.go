package assettype

import (
	"fmt"
	"sort"

	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/pkg/jsonscalar"
)

// AutomationContract is owned by one registered asset type. It declares the
// complete generic create surface and the non-secret subset safe for approval.
type AutomationContract struct {
	ConfigFields   []string
	ApprovalFields []string
	Normalize      func(map[string]any) error
	CredentialPlan func(map[string]any) (CredentialPlan, error)
	BindCredential func(map[string]any, CredentialBinding) (map[string]any, error)
}

type CredentialKind string

const (
	CredentialKindNone      CredentialKind = "none"
	CredentialKindReference CredentialKind = "reference"
	CredentialKindPassword  CredentialKind = "password"
	CredentialKindSSHKey    CredentialKind = "ssh_key"
)

// CredentialPlan is pure data for the later materialization service. Secret
// values remain write-only and never appear in PreparedCreate.Approval.
type CredentialPlan struct {
	Kind          CredentialKind
	ReferenceID   int64
	AcceptedTypes []string
	Plaintext     string
	PrivateKey    string
	Passphrase    string
	Username      string
	UsernameField string
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

type PreparedCreate struct {
	Handler    AssetTypeHandler
	Config     map[string]any
	Approval   map[string]any
	Credential CredentialPlan
}

// BindCredential applies a materialized credential through the selected type owner.
func (p PreparedCreate) BindCredential(binding CredentialBinding) (map[string]any, error) {
	if binding.ID <= 0 {
		return nil, fmt.Errorf("credential binding ID must be positive")
	}
	bind := p.Handler.AutomationContract().BindCredential
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

func PrepareCreate(assetType string, args map[string]any) (PreparedCreate, error) {
	prepared, contract, err := prepareAutomation(assetType, args)
	if err != nil {
		return PreparedCreate{}, err
	}
	if contract.Normalize != nil {
		if err := contract.Normalize(prepared.Config); err != nil {
			return PreparedCreate{}, err
		}
	}
	if err := normalizeAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	if err := prepared.Handler.ValidateCreateArgs(prepared.Config); err != nil {
		return PreparedCreate{}, err
	}
	if err := validateAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	return finalizeAutomation(prepared, contract)
}

// PrepareUpdate validates a partial update through the same type-owned field and
// credential declarations without applying create-only defaults or required fields.
func PrepareUpdate(assetType string, args map[string]any) (PreparedCreate, error) {
	prepared, contract, err := prepareAutomation(assetType, args)
	if err != nil {
		return PreparedCreate{}, err
	}
	if err := normalizeAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	if err := validateAutomation(prepared); err != nil {
		return PreparedCreate{}, err
	}
	return finalizeAutomation(prepared, contract)
}

func normalizeAutomation(prepared PreparedCreate) error {
	if normalizer, ok := prepared.Handler.(automationNormalizer); ok {
		return normalizer.NormalizeAutomationConfig(prepared.Config)
	}
	return nil
}

func validateAutomation(prepared PreparedCreate) error {
	if validator, ok := prepared.Handler.(automationValidator); ok {
		return validator.ValidateAutomationConfig(prepared.Config)
	}
	return nil
}

func finalizeAutomation(prepared PreparedCreate, contract AutomationContract) (PreparedCreate, error) {
	prepared.Approval = approvalView(prepared.Config, contract.ApprovalFields)
	credential, err := credentialPlan(contract, prepared.Config)
	if err != nil {
		return PreparedCreate{}, err
	}
	prepared.Credential = credential
	return prepared, nil
}

func prepareAutomation(assetType string, args map[string]any) (PreparedCreate, AutomationContract, error) {
	h, ok := Get(assetType)
	if !ok {
		return PreparedCreate{}, AutomationContract{}, fmt.Errorf("unsupported asset type %q (registered types: %s)", assetType, joinRegisteredTypes())
	}
	contract := h.AutomationContract()
	if len(contract.ConfigFields) == 0 {
		return PreparedCreate{}, AutomationContract{}, fmt.Errorf("asset type %q has no automation config contract", assetType)
	}
	config := cloneArgs(args)
	if err := rejectUnknownFields(config, contract.ConfigFields); err != nil {
		return PreparedCreate{}, AutomationContract{}, fmt.Errorf("invalid %s config: %w", assetType, err)
	}
	return PreparedCreate{Handler: h, Config: config}, contract, nil
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

func approvalView(args map[string]any, fields []string) map[string]any {
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

func passwordCredentialPlan(args map[string]any, plaintextField, usernameField string) (CredentialPlan, error) {
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
	return CredentialPlan{
		Kind:          CredentialKindPassword,
		Plaintext:     plaintext,
		Username:      ArgString(args, usernameField),
		UsernameField: usernameField,
	}, nil
}

func bindPasswordCredential(args map[string]any, binding CredentialBinding, plaintextField string) (map[string]any, error) {
	if binding.Type != credential_entity.TypePassword {
		return nil, fmt.Errorf("credential type %q is not accepted; expected password", binding.Type)
	}
	out := cloneArgs(args)
	delete(out, plaintextField)
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
