package permission

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/auditredact"
)

func TestParseApprovalResponseKindCapabilities(t *testing.T) {
	expected := []ApprovalItem{{Type: "exec", AssetID: 7, AssetName: "web-1", Command: "uptime"}}

	parsed, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "allow"}, expected)
	require.NoError(t, err)
	require.Equal(t, ApprovalAllow, parsed.Decision)

	parsed, err = ParseApprovalResponse(ApprovalKindOnce, ApprovalResponse{Decision: "allow"}, expected)
	require.NoError(t, err)
	require.Equal(t, ApprovalAllow, parsed.Decision)

	parsed, err = ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{
		Decision:    "allowAll",
		EditedItems: []ApprovalItem{{Type: "exec", AssetID: 7, AssetName: "web-1", Command: "uptime *"}},
	}, expected)
	require.NoError(t, err)
	require.Equal(t, ApprovalAllowAll, parsed.Decision)

	for _, kind := range []string{ApprovalKindOnce, ApprovalKindBatch, ApprovalKindGrant, ApprovalKindDelete, ApprovalKindExtension} {
		t.Run(kind+" rejects allowAll", func(t *testing.T) {
			parsed, err := ParseApprovalResponse(kind, ApprovalResponse{Decision: "allowAll"}, expected)
			require.Error(t, err)
			require.Equal(t, ApprovalDeny, parsed.Decision)
		})
	}
}

func TestParseApprovalResponseTreatsUnchangedCommandsAsSystemSubjects(t *testing.T) {
	expected := []ApprovalItem{{Type: "oss", AssetID: 7, AssetName: "s3-prod", Command: "object.read mybucket/secrets*"}}
	parsed, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{
		Decision:    "allowAll",
		EditedItems: append([]ApprovalItem(nil), expected...),
	}, expected)

	require.NoError(t, err)
	require.Equal(t, ApprovalAllowAll, parsed.Decision)
	require.Empty(t, parsed.EditedItems)
}

func TestParseApprovalResponseRejectsEditedScopeMutation(t *testing.T) {
	expected := []ApprovalItem{{Type: "exec", AssetID: 7, AssetName: "web-1", Command: "uptime"}}
	parsed, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{
		Decision:    "allowAll",
		EditedItems: []ApprovalItem{{Type: "exec", AssetID: 8, AssetName: "other", Command: "uptime"}},
	}, expected)

	require.Error(t, err)
	require.Equal(t, ApprovalDeny, parsed.Decision)
}

func TestSafeApprovalItemsRedactsSecretsAndReportsRedaction(t *testing.T) {
	items := []ApprovalItem{{
		Type: "exec", AssetID: 1, AssetName: "web-1",
		Command: "mysql -h localhost --password=hunter2",
		Detail:  "-----BEGIN PRIVATE KEY-----\nMIIEvQ\n-----END PRIVATE KEY-----",
	}}

	safe, redacted := SafeApprovalItems(items)

	require.True(t, redacted)
	require.NotContains(t, safe[0].Command, "hunter2")
	require.Contains(t, safe[0].Command, auditredact.RedactedValue)
	require.NotContains(t, safe[0].Detail, "PRIVATE KEY")
	require.Contains(t, safe[0].Detail, auditredact.RedactedValue)
	// 非敏感字段原样保留
	require.Equal(t, items[0].Type, safe[0].Type)
	require.Equal(t, items[0].AssetID, safe[0].AssetID)
	require.Equal(t, items[0].AssetName, safe[0].AssetName)
	// 原始 items 不得被改写
	require.Contains(t, items[0].Command, "hunter2")
	require.Contains(t, items[0].Detail, "PRIVATE KEY")
}

func TestSafeApprovalDescriptionRedactsCredentialMaterial(t *testing.T) {
	secret := "approval-description-" + "credential-sentinel"
	got := SafeApprovalDescription("deploy reason: Authorization: Basic " + secret)
	require.NotContains(t, got, secret)
	require.Contains(t, got, auditredact.RedactedValue)
	require.Contains(t, got, "deploy reason")
}

func TestSafeApprovalItemsCleanSubjectNoRedaction(t *testing.T) {
	items := []ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime"}}
	safe, redacted := SafeApprovalItems(items)
	require.False(t, redacted)
	require.Equal(t, items, safe)
}

func TestSafeApprovalItemsPreservesBracketPrefixedShellCommand(t *testing.T) {
	items := []ApprovalItem{{Type: "exec", AssetID: 1, Command: "[ -f /tmp/ready ] && echo ready"}}
	safe, redacted := SafeApprovalItems(items)
	require.False(t, redacted)
	require.Equal(t, items, safe)
}

func TestCanPersistGrantRedactionGating(t *testing.T) {
	expected := []ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime"}}
	edited := []ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime *"}}

	parsedAllowAll, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "allowAll", EditedItems: edited}, expected)
	require.NoError(t, err)
	parsedAllow, _ := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "allow"}, expected)
	parsedDeny, _ := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "deny"}, expected)
	parsedGrant, err := ParseApprovalResponse(ApprovalKindGrant, ApprovalResponse{Decision: "allow", EditedItems: edited}, expected)
	require.NoError(t, err)
	parsedBatch, _ := ParseApprovalResponse(ApprovalKindBatch, ApprovalResponse{Decision: "allow"}, expected)

	// 未脱敏：现有行为完全不变
	require.True(t, CanPersistGrant(false, ApprovalKindSingle, parsedAllowAll))
	require.True(t, CanPersistGrant(false, ApprovalKindSingle, parsedAllow))
	require.True(t, CanPersistGrant(false, ApprovalKindSingle, parsedDeny))
	require.True(t, CanPersistGrant(false, ApprovalKindGrant, parsedGrant))
	require.True(t, CanPersistGrant(false, ApprovalKindBatch, parsedBatch))

	// 脱敏：allowAll、grant/edited_items 全部拒绝；deny/allow-once 保留
	require.False(t, CanPersistGrant(true, ApprovalKindSingle, parsedAllowAll))
	require.False(t, CanPersistGrant(true, ApprovalKindGrant, parsedGrant))
	require.True(t, CanPersistGrant(true, ApprovalKindSingle, parsedAllow))
	require.True(t, CanPersistGrant(true, ApprovalKindSingle, parsedDeny))
	require.True(t, CanPersistGrant(true, ApprovalKindBatch, parsedBatch))
}
