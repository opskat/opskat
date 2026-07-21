package policy

import (
	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// checkMongoPolicyRules 检查 MongoDB 命令是否符合给定策略（不合并默认策略）。
//
// command 是完整的 mongo 命令串（`<op> [collection] [--db=…] [--query=…]`），
// 不是裸 operation：统一 exec 的 CanonicalizeMongoCommand 送进来的就是这个形式，
// 审批弹窗与审计看到的也是它。匹配交给 op 感知的 MatchMongoRule——用裸 op 全等
// 比较（policyValueMatches）会让所有内置 deny 在富命令串上静默失配。
func checkMongoPolicyRules(ctx context.Context, p *asset_entity.MongoPolicy, command string) aictx.CheckResult {
	if p == nil {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
	}
	for _, denied := range p.DenyTypes {
		if MatchMongoRule(denied, command) {
			return aictx.CheckResult{
				Decision:       aictx.Deny,
				Message:        PolicyFmt(ctx, "MongoDB operation %s denied by policy", "MongoDB 操作 %s 被策略禁止", command),
				DecisionSource: aictx.SourcePolicyDeny,
				MatchedPattern: denied,
			}
		}
	}
	if len(p.AllowTypes) > 0 {
		for _, allowed := range p.AllowTypes {
			if MatchMongoRule(allowed, command) {
				return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
			}
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
}

// CheckMongoDBPolicy 检查 MongoDB 命令是否符合策略（合并默认策略后检查）。
// command 的形式见 checkMongoPolicyRules；裸 operation 是它的合法子集
// （没有 collection 的操作，如 listDatabases，会渲染成一个只有 op 的串）。
// exec_mongo 旧工具路径同样传完整命令串——见 helper.HandleExecMongo，
// 它用 MongoCommand.Render() 而不是裸 operation 喂给 CheckForAsset。
func CheckMongoDBPolicy(ctx context.Context, p *asset_entity.MongoPolicy, command string) aictx.CheckResult {
	merged := EffectiveMongoPolicy(ctx, p)
	return checkMongoPolicyRules(ctx, merged, command)
}
