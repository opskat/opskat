package policy

import (
	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// checkMongoPolicyRules 检查 MongoDB 操作是否符合给定策略（不合并默认策略）
func checkMongoPolicyRules(ctx context.Context, p *asset_entity.MongoPolicy, operation string) aictx.CheckResult {
	if p == nil {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
	}
	for _, denied := range p.DenyTypes {
		if policyValueMatches(denied, operation) {
			return aictx.CheckResult{
				Decision:       aictx.Deny,
				Message:        PolicyFmt(ctx, "MongoDB operation %s denied by policy", "MongoDB 操作 %s 被策略禁止", operation),
				DecisionSource: aictx.SourcePolicyDeny,
				MatchedPattern: denied,
			}
		}
	}
	if len(p.AllowTypes) > 0 {
		for _, allowed := range p.AllowTypes {
			if policyValueMatches(allowed, operation) {
				return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
			}
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
}

// policy.CheckMongoDBPolicy 检查 MongoDB 操作是否符合策略（合并默认策略后检查）
func CheckMongoDBPolicy(ctx context.Context, p *asset_entity.MongoPolicy, operation string) aictx.CheckResult {
	merged := EffectiveMongoPolicy(ctx, p)
	return checkMongoPolicyRules(ctx, merged, operation)
}
