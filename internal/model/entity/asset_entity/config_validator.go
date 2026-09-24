package asset_entity

import (
	"context"
	"fmt"
	"sync"
)

// ConfigValidator 校验一个资产落库前的最终配置（a.Config 即将写进数据库的样子）。
type ConfigValidator func(ctx context.Context, a *Asset) error

// 运行期注册的资产配置校验器，按资产类型索引。
//
// 内置类型的配置校验写在 Validate 的类型分支里；扩展提供的资产类型在编译期不存在，
// 它们的校验规则在扩展自己的代码里（guest 的 RegisterConfigValidator），只能随扩展
// 加载注册进来。放在 entity 层是因为唯一的写入口 asset_svc 在 assettype 之下，
// 与 entity/policy 的 RegisterDefaultPolicy 是同一个做法。
var (
	configValidatorMu sync.RWMutex
	configValidators  = map[string]ConfigValidator{}
)

// RegisterConfigValidator 为 assetType 注册配置校验器。重复注册返回错误：一个类型
// 只有一个归属者，覆盖会让先注册者的规则静默失效。
func RegisterConfigValidator(assetType string, v ConfigValidator) error {
	configValidatorMu.Lock()
	defer configValidatorMu.Unlock()
	if _, exists := configValidators[assetType]; exists {
		return fmt.Errorf("config validator for asset type %q is already registered", assetType)
	}
	configValidators[assetType] = v
	return nil
}

// UnregisterConfigValidator 移除 assetType 的配置校验器（扩展禁用/卸载）。
func UnregisterConfigValidator(assetType string) {
	configValidatorMu.Lock()
	defer configValidatorMu.Unlock()
	delete(configValidators, assetType)
}

// ValidateRegisteredConfig 跑 a.Type 注册的配置校验器；没有注册的类型直接通过。
func ValidateRegisteredConfig(ctx context.Context, a *Asset) error {
	configValidatorMu.RLock()
	v, ok := configValidators[a.Type]
	configValidatorMu.RUnlock()
	if !ok {
		return nil
	}
	return v(ctx, a)
}
