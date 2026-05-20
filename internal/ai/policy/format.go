package policy

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
)

// FormatDenyMessage 生成拒绝命令的用户提示，含资产名、命令和提示规则。
func FormatDenyMessage(_ context.Context, assetName, command, reason string, hints []string) string {
	var sb strings.Builder
	if assetName != "" {
		fmt.Fprintf(&sb, "Command denied (%s).\nAsset: %s\nCommand: %s", reason, assetName, command)
	} else {
		fmt.Fprintf(&sb, "Command denied (%s).\nCommand: %s", reason, command)
	}
	if len(hints) > 0 {
		sb.WriteString("\n\nAllowed command patterns for this asset:\n")
		for _, h := range hints {
			fmt.Fprintf(&sb, "- %s\n", h)
		}
		sb.WriteString("\nPlease adjust the command accordingly and retry.")
	}
	return sb.String()
}

// ResolveGroupChain 递归获取组链（组 → 父组 → ... → 根），最大深度 5
func ResolveGroupChain(ctx context.Context, groupID int64) []*group_entity.Group {
	var chain []*group_entity.Group
	currentID := groupID
	for i := 0; i < 5 && currentID > 0; i++ {
		g, err := group_repo.Group().Find(ctx, currentID)
		if err != nil {
			break
		}
		chain = append(chain, g)
		currentID = g.ParentID
	}
	return chain
}

// StmtRawTexts 从 policy.ClassifyStatements 结果里取每条语句的原始 SQL 文本（含 trim），
// 用于按 statement 粒度送进组通用策略与 Grant 匹配。
func StmtRawTexts(stmts []StatementInfo) []string {
	out := make([]string, 0, len(stmts))
	for _, s := range stmts {
		if t := strings.TrimSpace(s.Raw); t != "" {
			out = append(out, t)
		}
	}
	return out
}
