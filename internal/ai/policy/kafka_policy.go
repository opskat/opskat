package policy

import (
	"context"
	"path"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// MatchKafkaRule matches canonical Kafka permission commands.
// Both rule and command must be exactly "<action> <resource>", for example:
// "topic.read orders-*" or "topic.config.write orders".
func MatchKafkaRule(rule, command string) bool {
	if isWildcardAll(rule) {
		_, _, ok := splitKafkaRule(command)
		return ok
	}

	ruleAction, ruleResource, ok := splitKafkaRule(rule)
	if !ok {
		return false
	}
	commandAction, commandResource, ok := splitKafkaRule(command)
	if !ok {
		return false
	}

	actionMatched, err := path.Match(strings.ToLower(ruleAction), strings.ToLower(commandAction))
	if err != nil {
		logger.Default().Warn("kafka policy action match", zap.String("pattern", ruleAction), zap.Error(err))
		return false
	}
	if !actionMatched {
		return false
	}

	resourceMatched, err := path.Match(ruleResource, commandResource)
	if err != nil {
		logger.Default().Warn("kafka policy resource match", zap.String("pattern", ruleResource), zap.Error(err))
		return false
	}
	return resourceMatched
}

func splitKafkaRule(value string) (action, resource string, ok bool) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func CheckKafkaPolicy(ctx context.Context, policy *asset_entity.KafkaPolicy, command string) aictx.CheckResult {
	merged := EffectiveKafkaPolicy(ctx, policy)
	return checkKafkaPolicyRules(ctx, merged, command)
}

func checkKafkaPolicyRules(ctx context.Context, policy *asset_entity.KafkaPolicy, command string) aictx.CheckResult {
	if policy == nil {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
	}
	for _, rule := range policy.DenyList {
		if MatchKafkaRule(rule, command) {
			return aictx.CheckResult{
				Decision:       aictx.Deny,
				Message:        PolicyFmt(ctx, "Kafka operation denied by policy: %s", "Kafka 操作被策略禁止: %s", command),
				DecisionSource: aictx.SourcePolicyDeny,
				MatchedPattern: rule,
			}
		}
	}
	if len(policy.AllowList) > 0 {
		for _, rule := range policy.AllowList {
			if MatchKafkaRule(rule, command) {
				return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow, MatchedPattern: rule}
			}
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
}
