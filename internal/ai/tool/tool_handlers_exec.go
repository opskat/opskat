package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

func handleRequestGrant(ctx context.Context, args map[string]any) (string, error) {
	itemsJSON := aictx.ArgString(args, "items")
	reason := aictx.ArgString(args, "reason")
	if itemsJSON == "" {
		return "", fmt.Errorf("missing required parameter: items")
	}

	var rawItems []struct {
		AssetID         int64  `json:"asset_id"`
		CommandPatterns string `json:"command_patterns"`
	}
	if err := json.Unmarshal([]byte(itemsJSON), &rawItems); err != nil {
		return "", fmt.Errorf("invalid items JSON: %w", err)
	}
	if len(rawItems) == 0 {
		return "", fmt.Errorf("items must not be empty")
	}

	var grantItems []permission.GrantItem
	for _, raw := range rawItems {
		if raw.AssetID == 0 {
			return "", fmt.Errorf("each item must have a non-zero asset_id")
		}
		var patterns []string
		for _, line := range strings.Split(raw.CommandPatterns, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				patterns = append(patterns, line)
			}
		}
		if len(patterns) == 0 {
			continue
		}
		grantItems = append(grantItems, permission.GrantItem{AssetID: raw.AssetID, Patterns: patterns})
	}
	if len(grantItems) == 0 {
		return "", fmt.Errorf("no valid command patterns provided")
	}

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}

	result := checker.SubmitGrantMulti(ctx, grantItems, reason)
	aictx.RecordDecision(ctx, result)
	return result.Message, nil
}
