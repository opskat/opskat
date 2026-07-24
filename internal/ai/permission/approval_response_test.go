package permission

import (
	"testing"

	"github.com/stretchr/testify/require"
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

func TestParseApprovalResponseRejectsEditedScopeMutation(t *testing.T) {
	expected := []ApprovalItem{{Type: "exec", AssetID: 7, AssetName: "web-1", Command: "uptime"}}
	parsed, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{
		Decision:    "allowAll",
		EditedItems: []ApprovalItem{{Type: "exec", AssetID: 8, AssetName: "other", Command: "uptime"}},
	}, expected)

	require.Error(t, err)
	require.Equal(t, ApprovalDeny, parsed.Decision)
}
