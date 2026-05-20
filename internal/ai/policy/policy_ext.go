package policy

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/aictx"
	"go.uber.org/zap"
)

// ExtensionPolicyRule represents the allow/deny action lists in an extension policy group's Policy JSON.
type ExtensionPolicyRule struct {
	AllowList []string `json:"allow_list"`
	DenyList  []string `json:"deny_list"`
}

// CheckExtensionPolicy resolves ext: prefixed policy groups and checks allow/deny action lists.
// Logic:
//   - If no groupIDs → aictx.NeedConfirm
//   - Fetch policy groups via fetchPolicyGroups (handles ext: prefix)
//   - Unmarshal each group's Policy JSON into ExtensionPolicyRule
//   - aictx.Deny takes precedence: if action in any deny_list → aictx.Deny
//   - Then check allow: if action in any allow_list → aictx.Allow
//   - Otherwise → aictx.NeedConfirm
func CheckExtensionPolicy(ctx context.Context, groupIDs []string, action string) aictx.CheckResult {
	if len(groupIDs) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

	groups := fetchPolicyGroups(ctx, groupIDs)
	if len(groups) == 0 {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}

	var allAllow []string
	var allDeny []string

	for _, pg := range groups {
		var rule ExtensionPolicyRule
		if err := json.Unmarshal([]byte(pg.Policy), &rule); err != nil {
			logger.Default().Warn("unmarshal extension policy group",
				zap.String("id", pg.BuiltinID), zap.Error(err))
			continue
		}
		allAllow = append(allAllow, rule.AllowList...)
		allDeny = append(allDeny, rule.DenyList...)
	}

	// aictx.Deny takes precedence
	if slices.Contains(allDeny, action) {
		return aictx.CheckResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourcePolicyDeny,
			Message:        "action denied by extension policy: " + action,
		}
	}

	// Then check allow
	if slices.Contains(allAllow, action) {
		return aictx.CheckResult{
			Decision:       aictx.Allow,
			DecisionSource: aictx.SourcePolicyAllow,
		}
	}

	return aictx.CheckResult{Decision: aictx.NeedConfirm}
}
