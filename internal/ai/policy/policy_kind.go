package policy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
)

// PolicyKind 是策略逻辑的规范化种类,是 policy 测试链路统一的 dispatch key。
// 规范词表定义在最底层的 entity/policy(PolicyKind*),这里 alias 复用;资产类型 / 前端
// policyType 经 ResolvePolicyKind(查 entity/policy 的 asset-kind 注册表)映射到它。
const (
	PolicyKindCommand = policyent.PolicyKindCommand
	PolicyKindQuery   = policyent.PolicyKindQuery
	PolicyKindRedis   = policyent.PolicyKindRedis
	PolicyKindMongo   = policyent.PolicyKindMongo
	PolicyKindKafka   = policyent.PolicyKindKafka
	PolicyKindK8s     = policyent.PolicyKindK8s
	PolicyKindEtcd    = policyent.PolicyKindEtcd
)

// policyKindHandler 每个 policyKind 的测试/解码处理器。
type policyKindHandler struct {
	// decode 把前端传入的策略 JSON 还原成对应的具体策略指针(*CommandPolicy 等)。
	decode func(raw []byte) (any, error)
	// test 用当前策略 + 资产组链测试命令;current 为 decode 的产物或 nil。
	test func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput
}

var kindRegistry = map[string]policyKindHandler{}

func registerPolicyKind(kind string, h policyKindHandler) {
	kindRegistry[kind] = h
}

func init() {
	registerPolicyKind(PolicyKindCommand, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.CommandPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			cp, _ := current.(*asset_entity.CommandPolicy)
			return testSSHPolicy(ctx, cp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindQuery, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.QueryPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			qp, _ := current.(*asset_entity.QueryPolicy)
			return testQueryPolicy(ctx, qp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindRedis, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.RedisPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			rp, _ := current.(*asset_entity.RedisPolicy)
			return testRedisPolicy(ctx, rp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindK8s, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.K8sPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			kp, _ := current.(*asset_entity.K8sPolicy)
			return testK8sPolicy(ctx, kp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindEtcd, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.EtcdPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			ep, _ := current.(*asset_entity.EtcdPolicy)
			return testEtcdPolicy(ctx, ep, groups, command)
		},
	})
	registerPolicyKind(PolicyKindMongo, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.MongoPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			mp, _ := current.(*asset_entity.MongoPolicy)
			return testMongoPolicy(ctx, mp, groups, command)
		},
	})
	registerPolicyKind(PolicyKindKafka, policyKindHandler{
		decode: func(raw []byte) (any, error) {
			var p asset_entity.KafkaPolicy
			err := json.Unmarshal(raw, &p)
			return &p, err
		},
		test: func(ctx context.Context, current any, groups []*group_entity.Group, command string) PolicyTestOutput {
			kp, _ := current.(*asset_entity.KafkaPolicy)
			return testKafkaPolicy(ctx, kp, groups, command)
		},
	})
}

// assetTypeAlias 前端/历史别名 → 规范资产类型(再经注册表/兜底解析)。
// 资产类型→kind 的主映射由 assettype handler 经 entity/policy 的 asset-kind 注册表派生,
// 不再手维护;这里只保留少数与 handler Type()/kind 名都不同的前端别名。
var assetTypeAlias = map[string]string{
	"kubernetes": PolicyKindK8s, // 前端 k8s 选择别名;policyType 实际发 "k8s"
}

// ResolvePolicyKind 把资产类型 / 前端 policyType 解析为已注册的 policyKind。
// 先解别名,再查 assettype handler 注册的 asset-kind 表,最后允许直接传 kind;
// 仅当目标 kind 有注册 handler 时返回 ok=true,未注册返回 false,
// 调用方据此保持 "unsupported policy type" 的既有行为。
func ResolvePolicyKind(s string) (string, bool) {
	if canon, ok := assetTypeAlias[s]; ok {
		s = canon
	}
	kind, ok := policyent.AssetKindOf(s)
	if !ok {
		kind = s // 允许直接传 kind
	}
	if _, has := kindRegistry[kind]; !has {
		return "", false
	}
	return kind, true
}

// DecodeCurrentPolicy 用对应 kind 的 handler 把策略 JSON 还原为具体策略指针。
func DecodeCurrentPolicy(kind string, raw []byte) (any, error) {
	h, ok := kindRegistry[kind]
	if !ok {
		return nil, fmt.Errorf("unsupported policy kind: %s", kind)
	}
	return h.decode(raw)
}
