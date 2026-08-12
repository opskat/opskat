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
	if custom == nil || custom.IsEmpty() {
		return expandQueryPolicy(ctx, asset_entity.DefaultQueryPolicy())
	}
	return expandQueryPolicy(ctx, custom)
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
	if custom == nil || custom.IsEmpty() {
		return expandRedisPolicy(ctx, asset_entity.DefaultRedisPolicy())
	}
	return expandRedisPolicy(ctx, custom)
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
	if custom == nil || custom.IsEmpty() {
		return expandEtcdPolicy(ctx, asset_entity.DefaultEtcdPolicy())
	}
	return expandEtcdPolicy(ctx, custom)
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
	if custom == nil || custom.IsEmpty() {
		return expandMongoPolicy(ctx, asset_entity.DefaultMongoPolicy())
	}
	return expandMongoPolicy(ctx, custom)
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
	if custom == nil || custom.IsEmpty() {
		return expandKafkaPolicy(ctx, asset_entity.DefaultKafkaPolicy())
	}
	return expandKafkaPolicy(ctx, custom)
}

func expandOSSPolicy(ctx context.Context, p *asset_entity.OSSPolicy) *asset_entity.OSSPolicy {
	out := &asset_entity.OSSPolicy{}
	if p == nil {
		return out
	}
	out.AllowList = append(out.AllowList, p.AllowList...)
	out.DenyList = append(out.DenyList, p.DenyList...)
	if len(p.Groups) > 0 {
		allow, deny := ResolveOSSGroups(ctx, p.Groups)
		out.AllowList = append(out.AllowList, allow...)
		out.DenyList = append(out.DenyList, deny...)
	}
	return out
}

func EffectiveOSSPolicy(ctx context.Context, custom *asset_entity.OSSPolicy) *asset_entity.OSSPolicy {
	if custom == nil || custom.IsEmpty() {
		return expandOSSPolicy(ctx, asset_entity.DefaultOSSPolicy())
	}
	return expandOSSPolicy(ctx, custom)
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
	if custom == nil || custom.IsEmpty() {
		return expandK8sPolicy(ctx, asset_entity.DefaultK8sPolicy())
	}
	return expandK8sPolicy(ctx, custom)
}
