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

func TestApprovalKindForType(t *testing.T) {
	tests := []struct {
		approvalType string
		want         string
	}{
		{approvalType: ApprovalTypeDelete, want: ApprovalKindDelete},
		{approvalType: "ext_tool", want: ApprovalKindExtension},
		{approvalType: "exec", want: ApprovalKindSingle},
		{approvalType: "create", want: ApprovalKindOnce},
	}
	for _, tt := range tests {
		t.Run(tt.approvalType, func(t *testing.T) {
			require.Equal(t, tt.want, ApprovalKindForType(tt.approvalType))
		})
	}
}
