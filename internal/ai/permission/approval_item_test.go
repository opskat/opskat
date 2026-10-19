package permission

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ApprovalItemFor is the one approval-item builder every single approval shares —
// the AI's HandleConfirm, opsctl's terminal prompt, its desktop request and its
// NEEDS AUTHORIZATION — so a human is asked about the same item wherever they are.

const extItemCommand = "request --method=DELETE --path=/logs-app"

func TestApprovalItemForClassifiedExtensionCall(t *testing.T) {
	registerClassifiedExtType(t, "logs-app")

	item := ApprovalItemFor(context.Background(), 3, "es-logs", extEditTestType, extItemCommand,
		"opsctl exec es-logs -- "+extItemCommand)

	assert.Equal(t, extEditTestType, item.Type)
	assert.Equal(t, int64(3), item.AssetID)
	assert.Equal(t, "es-logs", item.AssetName)
	assert.Equal(t, extItemCommand, item.Command)
	assert.Equal(t, "delete", item.Action)
	assert.Equal(t, "logs-app", item.Resource)
	assert.Equal(t, []string{"logs-app"}, item.Resources)
	assert.Equal(t, "delete:logs-app", item.RememberPattern)
	// The approval surfaces render a classified item's detail as the tool call; a
	// caller's own display line must not replace it.
	var detail struct {
		Tool string `json:"tool"`
	}
	require.NoError(t, json.Unmarshal([]byte(item.Detail), &detail), "detail: %q", item.Detail)
	assert.Equal(t, "request", detail.Tool)
}

func TestApprovalItemForBuiltinTypeIsThePlainItem(t *testing.T) {
	item := ApprovalItemFor(context.Background(), 3, "web-1", asset_entity.AssetTypeSSH, "uptime", "opsctl exec web-1 -- uptime")

	assert.Equal(t, ApprovalItem{
		Type: "exec", AssetID: 3, AssetName: "web-1", Command: "uptime", Detail: "opsctl exec web-1 -- uptime",
	}, item)
}

// The subjects of an approval are what a terminal "always allow" lands as permanent
// rules and what NEEDS AUTHORIZATION tells a human to allow: the shape
// `opsctl policy allow` takes on that asset.
func TestApprovalRuleSubjects(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		resources []string
		want      []string
	}{
		{"one resource", []string{"logs-app"}, []string{"delete:logs-app"}},
		{"several resources share the Remember rule", []string{"logs-app", "logs-web"}, []string{"delete:logs-*"}},
		// A permanent rule cannot hold "<action>:" with an empty glob; a call touching
		// no resource is covered by its bare action — ext:<type>:<action>.
		{"no resource", nil, []string{"delete"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registerClassifiedExtType(t, tc.resources...)
			item := ApprovalItemFor(ctx, 3, "es-logs", extEditTestType, extItemCommand)
			assert.Equal(t, tc.want, ApprovalRuleSubjects(extEditTestType, item))
		})
	}

	t.Run("an extension call that did not classify has no rule", func(t *testing.T) {
		require.NoError(t, RegisterPolicyCheck(extEditTestType,
			func(context.Context, int64, string) aictx.CheckResult {
				return aictx.CheckResult{Decision: aictx.NeedConfirm}
			},
			func(context.Context, string) (ExtensionClassification, bool) { return ExtensionClassification{}, false }))
		t.Cleanup(func() { UnregisterPolicyCheck(extEditTestType) })
		item := ApprovalItemFor(ctx, 3, "es-logs", extEditTestType, extItemCommand)
		assert.Empty(t, ApprovalRuleSubjects(extEditTestType, item))
	})

	t.Run("built-in types keep their normalized subjects", func(t *testing.T) {
		item := ApprovalItemFor(ctx, 3, "web-1", "exec", "echo hi; echo bye")
		assert.Equal(t, []string{"echo hi", "echo bye"}, ApprovalRuleSubjects("exec", item))
	})
}
