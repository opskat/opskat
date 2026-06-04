package policy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
)

// PolicyKind 是策略逻辑的规范化种类,是 policy 测试链路统一的 dispatch key。
// 取值与 policy_group_entity.PolicyType*（command/query/redis/mongo/kafka/etcd）保持一致,额外加上 k8s。
// 资产类型 / 前端 policyType 经 ResolvePolicyKind 映射到它。
const (
	PolicyKindCommand = "command"
	PolicyKindQuery   = "query"
	PolicyKindRedis   = "redis"
	PolicyKindMongo   = "mongo"
	PolicyKindKafka   = "kafka"
	PolicyKindK8s     = "k8s"
	PolicyKindEtcd    = "etcd"
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
}

// assetTypeToKind 把资产类型 / 前端 policyType 字符串映射到规范 policyKind。
var assetTypeToKind = map[string]string{
	"ssh":        PolicyKindCommand,
	"serial":     PolicyKindCommand,
	"local":      PolicyKindCommand,
	"database":   PolicyKindQuery,
	"redis":      PolicyKindRedis,
	"mongo":      PolicyKindMongo,
	"mongodb":    PolicyKindMongo,
	"kafka":      PolicyKindKafka,
	"k8s":        PolicyKindK8s,
	"kubernetes": PolicyKindK8s,
	"etcd":       PolicyKindEtcd,
}

// ResolvePolicyKind 把资产类型 / 前端 policyType 解析为已注册的 policyKind。
// 仅当目标 kind 有注册 handler 时返回 ok=true;未注册(如当前的 mongo/kafka)返回 false,
// 调用方据此保持 "unsupported policy type" 的既有行为。
func ResolvePolicyKind(s string) (string, bool) {
	kind, ok := assetTypeToKind[s]
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
