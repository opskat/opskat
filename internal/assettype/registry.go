// internal/assettype/registry.go
package assettype

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
)

// AssetTypeHandler 资产类型处理器接口。
type AssetTypeHandler interface {
	Type() string
	DefaultPort() int
	SafeView(a *asset_entity.Asset) map[string]any
	ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error)
	DefaultPolicy() any
	// PolicyKind 返回该资产类型所用的规范 policyKind(见 entity/policy.PolicyKind*）。
	// 经 Register 写入 entity/policy 的 asset-kind 注册表,供 ai/policy.ResolvePolicyKind 派生。
	PolicyKind() string
	// AutomationContract declares the type-owned generic create and credential seam.
	AutomationContract() AutomationContract
	// ValidateCreateArgs 校验 AI 工具创建资产时的必填字段。
	// 由 put_asset 的 createAsset 在 ApplyCreateArgs 之前调用，每种类型自行声明所需字段。
	ValidateCreateArgs(args map[string]any) error
	ApplyCreateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error
	ApplyUpdateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error
}

// AuthenticationAssociation is a non-secret, type-owned pointer to managed authentication.
type AuthenticationAssociation struct {
	Type        string
	Ref         string
	Fingerprint string
}

type authenticationAssociationOwner interface {
	AuthenticationAssociation(a *asset_entity.Asset) (AuthenticationAssociation, bool, error)
}

// AuthenticationAssociationOf delegates association extraction to the registered type owner.
func AuthenticationAssociationOf(h AssetTypeHandler, a *asset_entity.Asset) (AuthenticationAssociation, bool, error) {
	owner, ok := h.(authenticationAssociationOwner)
	if !ok {
		return AuthenticationAssociation{}, false, nil
	}
	return owner.AuthenticationAssociation(a)
}

func passwordAuthenticationAssociation(id int64) (AuthenticationAssociation, bool, error) {
	if id <= 0 {
		return AuthenticationAssociation{}, false, nil
	}
	return AuthenticationAssociation{Type: credential_entity.TypePassword, Ref: fmt.Sprintf("credential:%d", id)}, true, nil
}

// validateRemoteServerArgs 是 ssh/database/redis/mongodb 共用的 host/port/username 校验。
func validateRemoteServerArgs(args map[string]any) error {
	if ArgString(args, "host") == "" || ArgInt(args, "port") == 0 || ArgString(args, "username") == "" {
		return fmt.Errorf("missing required parameters: host, port, username")
	}
	return nil
}

var (
	mu       sync.RWMutex
	registry = map[string]AssetTypeHandler{}
)

// Register 注册内置资产类型处理器。冲突或缺少自动化契约直接 panic —— 内置注册发生在
// init()，两者都是启动期编程错误，不该被静默覆盖。
//
// 运行期可增删的类型（扩展提供的资产类型）走 RegisterDynamic / Unregister：那条路上的
// 冲突来自用户装了两个声明同一类型的扩展，必须被响亮拒绝，但不能让整个应用崩掉。
func Register(h AssetTypeHandler) {
	if err := register(h); err != nil {
		panic("assettype: " + err.Error())
	}
}

// RegisterDynamic 注册一个运行期可再移除的资产类型处理器，冲突返回错误。
func RegisterDynamic(h AssetTypeHandler) error {
	return register(h)
}

// Unregister 移除一个已注册的资产类型处理器，连同它写进 entity/policy 的 asset-kind
// 映射。只有 RegisterDynamic 注册进来的类型会被移除——内置类型没有任何调用方会去
// 注销它们。
func Unregister(assetType string) {
	mu.Lock()
	delete(registry, assetType)
	mu.Unlock()
	policy.UnregisterAssetKind(assetType)
}

func register(h AssetTypeHandler) error {
	contract := h.AutomationContract()
	if len(contract.ConfigFields) == 0 {
		return fmt.Errorf("asset type %q must declare automation config fields", h.Type())
	}
	mu.Lock()
	if _, exists := registry[h.Type()]; exists {
		mu.Unlock()
		return fmt.Errorf("asset type %q is already registered", h.Type())
	}
	registry[h.Type()] = h
	mu.Unlock()
	if kind := h.PolicyKind(); kind != "" {
		policy.RegisterAssetKind(h.Type(), kind)
	}
	return nil
}

func Get(assetType string) (AssetTypeHandler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	h, ok := registry[assetType]
	return h, ok
}

func All() []AssetTypeHandler {
	mu.RLock()
	defer mu.RUnlock()
	types := make([]string, 0, len(registry))
	for assetType := range registry {
		types = append(types, assetType)
	}
	sort.Strings(types)
	out := make([]AssetTypeHandler, 0, len(types))
	for _, assetType := range types {
		out = append(out, registry[assetType])
	}
	return out
}

// --- Arg extraction helpers ---

// ArgString 从 args 中严格解析字符串：仅当值就是 string 时返回其值；缺失、nil、数字、
// 布尔与一切复合值（map/slice/array/struct/pointer）一律返回空串，绝不用 fmt.Sprintf
// 字符串化——那会让藏了嵌套 secret 的复合 host/username 混过“必填”校验。
func ArgString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func ArgInt(args map[string]any, key string) int {
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func ArgInt64(args map[string]any, key string) int64 {
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		return 0
	}
}

// ArgBool 从 args 中解析 bool。支持 bool、字符串 ("true"/"1"/"yes")、数字 1。
func ArgBool(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true") || x == "1" || strings.EqualFold(x, "yes")
	default:
		return fmt.Sprintf("%v", x) == "1"
	}
}

// ArgStringSlice 从 args 中解析字符串数组。支持 []string、[]any（且每一项都是 string）、
// 用逗号/分号/换行分隔的字符串。自动 trim 空白并丢弃空项。[]any 含任一非字符串项
// （嵌套 map/数字/布尔/切片）整体拒绝返回 nil，绝不用 fmt.Sprintf 把项字符串化——那会让
// 藏了嵌套 secret 的 brokers/endpoints 项混过“必填数组”校验。
func ArgStringSlice(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return cleanStrings(x)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return cleanStrings(out)
	case string:
		parts := strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == '\n' || r == ';' })
		return cleanStrings(parts)
	default:
		return nil
	}
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
