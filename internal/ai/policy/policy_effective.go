package policy

import (
	"context"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func isWildcardAll(rule string) bool {
	return strings.TrimSpace(rule) == "*"
}

func policyValueMatches(rule, value string) bool {
	return isWildcardAll(rule) || strings.EqualFold(strings.TrimSpace(rule), strings.TrimSpace(value))
}

func containsPolicyValue(rules []string, value string) bool {
	for _, rule := range rules {
		if policyValueMatches(rule, value) {
			return true
		}
	}
	return false
}

func expandQueryPolicy(ctx context.Context, p *asset_entity.QueryPolicy) *asset_entity.QueryPolicy {
	out := &asset_entity.QueryPolicy{}
	if p == nil {
		return out
	}
	out.AllowTypes = append(out.AllowTypes, p.AllowTypes...)
	out.DenyTypes = append(out.DenyTypes, p.DenyTypes...)
	out.DenyFlags = append(out.DenyFlags, p.DenyFlags...)
	if len(p.Groups) > 0 {
		allowTypes, denyTypes, denyFlags := ResolveQueryGroups(ctx, p.Groups)
		out.AllowTypes = append(out.AllowTypes, allowTypes...)
		out.DenyTypes = append(out.DenyTypes, denyTypes...)
		out.DenyFlags = append(out.DenyFlags, denyFlags...)
	}
	return out
}

func EffectiveQueryPolicy(ctx context.Context, custom *asset_entity.QueryPolicy) *asset_entity.QueryPolicy {
	custom = expandQueryPolicy(ctx, custom)
	defaults := expandQueryPolicy(ctx, asset_entity.DefaultQueryPolicy())

	out := &asset_entity.QueryPolicy{}
	if len(custom.AllowTypes) > 0 {
		out.AllowTypes = AppendUnique(out.AllowTypes, custom.AllowTypes...)
	} else {
		out.AllowTypes = AppendUnique(out.AllowTypes, defaults.AllowTypes...)
	}
	out.DenyTypes = AppendUnique(out.DenyTypes, custom.DenyTypes...)
	out.DenyTypes = AppendUnique(out.DenyTypes, defaults.DenyTypes...)
	out.DenyFlags = AppendUnique(out.DenyFlags, custom.DenyFlags...)
	out.DenyFlags = AppendUnique(out.DenyFlags, defaults.DenyFlags...)
	return out
}

func expandRedisPolicy(ctx context.Context, p *asset_entity.RedisPolicy) *asset_entity.RedisPolicy {
	out := &asset_entity.RedisPolicy{}
	if p == nil {
		return out
	}
	out.AllowList = append(out.AllowList, p.AllowList...)
	out.DenyList = append(out.DenyList, p.DenyList...)
	if len(p.Groups) > 0 {
		allow, deny := ResolveRedisGroups(ctx, p.Groups)
		out.AllowList = append(out.AllowList, allow...)
		out.DenyList = append(out.DenyList, deny...)
	}
	return out
}

func EffectiveRedisPolicy(ctx context.Context, custom *asset_entity.RedisPolicy) *asset_entity.RedisPolicy {
	custom = expandRedisPolicy(ctx, custom)
	defaults := expandRedisPolicy(ctx, asset_entity.DefaultRedisPolicy())

	out := &asset_entity.RedisPolicy{}
	if len(custom.AllowList) > 0 {
		out.AllowList = AppendUnique(out.AllowList, custom.AllowList...)
	} else {
		out.AllowList = AppendUnique(out.AllowList, defaults.AllowList...)
	}
	out.DenyList = AppendUnique(out.DenyList, custom.DenyList...)
	out.DenyList = AppendUnique(out.DenyList, defaults.DenyList...)
	return out
}

func expandEtcdPolicy(ctx context.Context, p *asset_entity.EtcdPolicy) *asset_entity.EtcdPolicy {
	out := &asset_entity.EtcdPolicy{}
	if p == nil {
		return out
	}
	out.AllowList = append(out.AllowList, p.AllowList...)
	out.DenyList = append(out.DenyList, p.DenyList...)
	if len(p.Groups) > 0 {
		allow, deny := ResolveEtcdGroups(ctx, p.Groups)
		out.AllowList = append(out.AllowList, allow...)
		out.DenyList = append(out.DenyList, deny...)
	}
	return out
}

func EffectiveEtcdPolicy(ctx context.Context, custom *asset_entity.EtcdPolicy) *asset_entity.EtcdPolicy {
	custom = expandEtcdPolicy(ctx, custom)
	defaults := expandEtcdPolicy(ctx, asset_entity.DefaultEtcdPolicy())

	out := &asset_entity.EtcdPolicy{}
	if len(custom.AllowList) > 0 {
		out.AllowList = AppendUnique(out.AllowList, custom.AllowList...)
	} else {
		out.AllowList = AppendUnique(out.AllowList, defaults.AllowList...)
	}
	out.DenyList = AppendUnique(out.DenyList, custom.DenyList...)
	out.DenyList = AppendUnique(out.DenyList, defaults.DenyList...)
	return out
}

func expandMongoPolicy(ctx context.Context, p *asset_entity.MongoPolicy) *asset_entity.MongoPolicy {
	out := &asset_entity.MongoPolicy{}
	if p == nil {
		return out
	}
	out.AllowTypes = append(out.AllowTypes, p.AllowTypes...)
	out.DenyTypes = append(out.DenyTypes, p.DenyTypes...)
	if len(p.Groups) > 0 {
		allowTypes, denyTypes := ResolveMongoGroups(ctx, p.Groups)
		out.AllowTypes = append(out.AllowTypes, allowTypes...)
		out.DenyTypes = append(out.DenyTypes, denyTypes...)
	}
	return out
}

func EffectiveMongoPolicy(ctx context.Context, custom *asset_entity.MongoPolicy) *asset_entity.MongoPolicy {
	custom = expandMongoPolicy(ctx, custom)
	defaults := expandMongoPolicy(ctx, asset_entity.DefaultMongoPolicy())

	out := &asset_entity.MongoPolicy{}
	if len(custom.AllowTypes) > 0 {
		out.AllowTypes = AppendUnique(out.AllowTypes, custom.AllowTypes...)
	} else {
		out.AllowTypes = AppendUnique(out.AllowTypes, defaults.AllowTypes...)
	}
	out.DenyTypes = AppendUnique(out.DenyTypes, custom.DenyTypes...)
	out.DenyTypes = AppendUnique(out.DenyTypes, defaults.DenyTypes...)
	return out
}

func expandKafkaPolicy(ctx context.Context, p *asset_entity.KafkaPolicy) *asset_entity.KafkaPolicy {
	out := &asset_entity.KafkaPolicy{}
	if p == nil {
		return out
	}
	out.AllowList = append(out.AllowList, p.AllowList...)
	out.DenyList = append(out.DenyList, p.DenyList...)
	if len(p.Groups) > 0 {
		allow, deny := ResolveKafkaGroups(ctx, p.Groups)
		out.AllowList = append(out.AllowList, allow...)
		out.DenyList = append(out.DenyList, deny...)
	}
	return out
}

func EffectiveKafkaPolicy(ctx context.Context, custom *asset_entity.KafkaPolicy) *asset_entity.KafkaPolicy {
	custom = expandKafkaPolicy(ctx, custom)
	defaults := expandKafkaPolicy(ctx, asset_entity.DefaultKafkaPolicy())

	out := &asset_entity.KafkaPolicy{}
	if len(custom.AllowList) > 0 {
		out.AllowList = AppendUnique(out.AllowList, custom.AllowList...)
	} else {
		out.AllowList = AppendUnique(out.AllowList, defaults.AllowList...)
	}
	out.DenyList = AppendUnique(out.DenyList, custom.DenyList...)
	out.DenyList = AppendUnique(out.DenyList, defaults.DenyList...)
	return out
}

func expandK8sPolicy(ctx context.Context, p *asset_entity.K8sPolicy) *asset_entity.K8sPolicy {
	out := &asset_entity.K8sPolicy{}
	if p == nil {
		return out
	}
	out.AllowList = append(out.AllowList, p.AllowList...)
	out.DenyList = append(out.DenyList, p.DenyList...)
	if len(p.Groups) > 0 {
		allow, deny := ResolveCommandGroups(ctx, p.Groups)
		out.AllowList = append(out.AllowList, allow...)
		out.DenyList = append(out.DenyList, deny...)
	}
	return out
}

func EffectiveK8sPolicy(ctx context.Context, custom *asset_entity.K8sPolicy) *asset_entity.K8sPolicy {
	custom = expandK8sPolicy(ctx, custom)
	defaults := expandK8sPolicy(ctx, asset_entity.DefaultK8sPolicy())

	out := &asset_entity.K8sPolicy{}
	if len(custom.AllowList) > 0 {
		out.AllowList = AppendUnique(out.AllowList, custom.AllowList...)
	} else {
		out.AllowList = AppendUnique(out.AllowList, defaults.AllowList...)
	}
	out.DenyList = AppendUnique(out.DenyList, custom.DenyList...)
	out.DenyList = AppendUnique(out.DenyList, defaults.DenyList...)
	return out
}
