package policy

import (
	"context"
	"sync"
)

// PolicyKind* 是 policy 逻辑的规范化种类词表,是资产轴与 policy 轴的唯一映射目标。
// ai/policy.PolicyKind* 与 policy_group_entity.PolicyType* alias 到这里。
const (
	PolicyKindCommand = "command"
	PolicyKindQuery   = "query"
	PolicyKindRedis   = "redis"
	PolicyKindMongo   = "mongo"
	PolicyKindKafka   = "kafka"
	PolicyKindK8s     = "k8s"
	PolicyKindEtcd    = "etcd"
	PolicyKindOSS     = "oss"
)

// assetKindRegistry 资产类型 → 规范 policyKind。由 assettype.Register 在 handler 注册时
// 经 h.PolicyKind() 写入,替代 ai/policy 里手维护的 assetTypeToKind 字面量。
var assetKindRegistry = struct {
	sync.RWMutex
	kinds map[string]string
}{
	kinds: make(map[string]string),
}

// RegisterAssetKind 注册资产类型所用的 policyKind。
func RegisterAssetKind(assetType, kind string) {
	assetKindRegistry.Lock()
	defer assetKindRegistry.Unlock()
	assetKindRegistry.kinds[assetType] = kind
}

// UnregisterAssetKind 注销资产类型的 policyKind(测试用)。
func UnregisterAssetKind(assetType string) {
	assetKindRegistry.Lock()
	defer assetKindRegistry.Unlock()
	delete(assetKindRegistry.kinds, assetType)
}

// AssetKindOf 返回资产类型对应的 policyKind 及是否已注册。
func AssetKindOf(assetType string) (string, bool) {
	assetKindRegistry.RLock()
	defer assetKindRegistry.RUnlock()
	k, ok := assetKindRegistry.kinds[assetType]
	return k, ok
}

var defaultPolicyRegistry = struct {
	sync.RWMutex
	providers map[string]func() any
}{
	providers: make(map[string]func() any),
}

// RegisterDefaultPolicy 注册资产类型的默认策略提供者。
// 内置类型在 init() 中注册，扩展类型在 Bridge.Register 时注册。
func RegisterDefaultPolicy(assetType string, provider func() any) {
	defaultPolicyRegistry.Lock()
	defer defaultPolicyRegistry.Unlock()
	defaultPolicyRegistry.providers[assetType] = provider
}

// UnregisterDefaultPolicy 注销资产类型的默认策略提供者。
func UnregisterDefaultPolicy(assetType string) {
	defaultPolicyRegistry.Lock()
	defer defaultPolicyRegistry.Unlock()
	delete(defaultPolicyRegistry.providers, assetType)
}

// GetDefaultPolicyOf 获取指定资产类型的默认策略。
// 返回策略结构体和是否找到。
func GetDefaultPolicyOf(assetType string) (any, bool) {
	defaultPolicyRegistry.RLock()
	defer defaultPolicyRegistry.RUnlock()
	fn, ok := defaultPolicyRegistry.providers[assetType]
	if !ok {
		return nil, false
	}
	return fn(), true
}

// AssetDefaultPolicyProvider 按资产自身的配置给出默认策略，用于默认策略不由类型
// 单独决定的资产（通用资产复制其自定义类型上的默认规则）。assetConfig 是资产的
// Config JSON。
type AssetDefaultPolicyProvider func(ctx context.Context, assetConfig string) (any, error)

var assetDefaultPolicyRegistry = struct {
	sync.RWMutex
	providers map[string]AssetDefaultPolicyProvider
}{
	providers: make(map[string]AssetDefaultPolicyProvider),
}

// RegisterAssetDefaultPolicy 注册资产类型的按资产默认策略提供者。
func RegisterAssetDefaultPolicy(assetType string, provider AssetDefaultPolicyProvider) {
	assetDefaultPolicyRegistry.Lock()
	defer assetDefaultPolicyRegistry.Unlock()
	assetDefaultPolicyRegistry.providers[assetType] = provider
}

// UnregisterAssetDefaultPolicy 注销资产类型的按资产默认策略提供者（测试用）。
func UnregisterAssetDefaultPolicy(assetType string) {
	assetDefaultPolicyRegistry.Lock()
	defer assetDefaultPolicyRegistry.Unlock()
	delete(assetDefaultPolicyRegistry.providers, assetType)
}

// DefaultPolicyForAsset 返回新建资产时应写入的默认策略：有按资产的提供者时用它
// （错误原样返回），否则用 GetDefaultPolicyOf 的按类型默认策略。ok 为 false 表示
// 该类型没有默认策略。
func DefaultPolicyForAsset(ctx context.Context, assetType, assetConfig string) (p any, ok bool, err error) {
	assetDefaultPolicyRegistry.RLock()
	provider, found := assetDefaultPolicyRegistry.providers[assetType]
	assetDefaultPolicyRegistry.RUnlock()
	if !found {
		p, ok = GetDefaultPolicyOf(assetType)
		return p, ok, nil
	}
	p, err = provider(ctx, assetConfig)
	if err != nil {
		return nil, false, err
	}
	return p, true, nil
}

func init() {
	RegisterDefaultPolicy("ssh", func() any { return DefaultCommandPolicy() })
	RegisterDefaultPolicy("serial", func() any { return DefaultCommandPolicy() })
	RegisterDefaultPolicy("database", func() any { return DefaultQueryPolicy() })
	RegisterDefaultPolicy("redis", func() any { return DefaultRedisPolicy() })
	RegisterDefaultPolicy("mongodb", func() any { return DefaultMongoPolicy() })
	RegisterDefaultPolicy("kafka", func() any { return DefaultKafkaPolicy() })
	RegisterDefaultPolicy("k8s", func() any { return DefaultK8sPolicy() })
	RegisterDefaultPolicy("oss", func() any { return DefaultOSSPolicy() })
}
