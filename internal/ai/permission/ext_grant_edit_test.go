package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// An extension call classified by check_policy is granted by its classification
// (ext:<type>:<action>:<resource>), never by its command text. The "Remember" editor
// therefore has to show — and hand back — that classification's <action>:<resource>
// tail: the value it is pre-filled with, the value the user edits, and the value that
// is persisted must all be the same string.

const extEditTestType = "esverify-edit-test"

// registerClassifiedExtType registers an extension-like type whose every command
// classifies as (delete, resources) under policy type "esverify". resources are in
// the host's glob form (pkg/extension quotes a literal resource).
func registerClassifiedExtType(t *testing.T, resources ...string) {
	t.Helper()
	require.NoError(t, RegisterPolicyCheck(extEditTestType,
		func(context.Context, int64, string) aictx.CheckResult {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		},
		func(context.Context, string) (ExtensionClassification, bool) {
			return ExtensionClassification{PolicyType: "esverify", Action: "delete", Resources: resources, Tool: "request"}, true
		}))
	t.Cleanup(func() { UnregisterPolicyCheck(extEditTestType) })
}

// confirmWithGrantRepo wires the repos HandleConfirm touches and returns the grant
// repo "always allow" persists into.
func confirmWithGrantRepo(t *testing.T) (context.Context, *stubGrantRepo) {
	t.Helper()
	_ = withStubAudit(t)
	ctx, mockAsset, _ := setupPolicyTest(t)
	mockAsset.EXPECT().Find(gomock.Any(), int64(3)).
		Return(&asset_entity.Asset{ID: 3, Name: "es-logs", Type: extEditTestType}, nil).AnyTimes()
	stub := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() { grant_repo.RegisterGrant(orig) })
	return aictx.WithSessionID(ctx, "sess-edit"), stub
}

func persistedCommands(stub *stubGrantRepo) []string {
	items := stub.items["sess-edit"]
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Command)
	}
	return out
}

func TestClassifiedApprovalItemCarriesTheGrantTailToEdit(t *testing.T) {
	cases := []struct{ resource, shown, want string }{
		{"logs-app", "logs-app", "delete:logs-app"},
		// A literal resource arrives quoted (the extension returned "logs-*[1]"): the
		// dialog shows it as returned, and the pre-filled tail keeps the quoting, so
		// the untouched value still grants only the literal resource the user saw.
		{`logs-\*\[1]`, "logs-*[1]", `delete:logs-\*\[1]`},
	}
	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			registerClassifiedExtType(t, tc.resource)
			ctx, stub := confirmWithGrantRepo(t)

			var shown ApprovalItem
			checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
				shown = items[0]
				return ApprovalResponse{Decision: "allowAll"}
			})
			got := checker.HandleConfirm(ctx, 3, extEditTestType, "request --method=DELETE --path=/logs-app")

			require.Equal(t, aictx.Allow, got.Decision)
			require.Equal(t, tc.shown, shown.Resource)
			require.Equal(t, []string{tc.shown}, shown.Resources)
			require.Equal(t, tc.want, shown.RememberPattern)
			require.Equal(t, []string{"ext:esverify:" + tc.want}, persistedCommands(stub),
				"an unedited Remember persists exactly the value the editor was pre-filled with")
		})
	}
}

// A multi-resource item carries every resource as the extension returned it. Until
// a multi-resource Remember pre-fill exists, the item offers none, and an unedited
// "always allow" persists one exact grant per resource.
func TestMultiResourceApprovalCarriesEveryResource(t *testing.T) {
	registerClassifiedExtType(t, "logs-*", `a\[1\]`, "prod-1")
	ctx, stub := confirmWithGrantRepo(t)

	var shown ApprovalItem
	checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
		shown = items[0]
		return ApprovalResponse{Decision: "allowAll"}
	})
	got := checker.HandleConfirm(ctx, 3, extEditTestType, "request --method=DELETE --path=/logs-*,a[1],prod-1")

	require.Equal(t, aictx.Allow, got.Decision)
	require.Equal(t, "delete", shown.Action)
	require.Equal(t, []string{"logs-*", "a[1]", "prod-1"}, shown.Resources)
	require.Empty(t, shown.Resource)
	require.Empty(t, shown.RememberPattern)
	require.Equal(t, []string{
		"ext:esverify:delete:logs-*", `ext:esverify:delete:a\[1\]`, "ext:esverify:delete:prod-1",
	}, persistedCommands(stub))
}

func TestClassifiedApprovalPersistsTheEditedGrantTail(t *testing.T) {
	registerClassifiedExtType(t, "logs-app")
	ctx, stub := confirmWithGrantRepo(t)

	checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
		edited := items[0]
		edited.Command = "delete:logs-*"
		return ApprovalResponse{Decision: "allowAll", EditedItems: []ApprovalItem{edited}}
	})
	got := checker.HandleConfirm(ctx, 3, extEditTestType, "request --method=DELETE --path=/logs-app")

	require.Equal(t, aictx.Allow, got.Decision)
	require.Equal(t, []string{"ext:esverify:delete:logs-*"}, persistedCommands(stub),
		"the user's glob is intentional: the edited tail is persisted verbatim")
}

func TestClassifiedApprovalRejectsAnEditThatDropsTheAction(t *testing.T) {
	for _, edit := range []string{"*", ":logs-app", "get:logs-app", "delete", "delete:logs-[", "request --method=DELETE --path=/logs-*"} {
		t.Run(edit, func(t *testing.T) {
			registerClassifiedExtType(t, "logs-app")
			ctx, stub := confirmWithGrantRepo(t)

			checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
				edited := items[0]
				edited.Command = edit
				return ApprovalResponse{Decision: "allowAll", EditedItems: []ApprovalItem{edited}}
			})
			got := checker.HandleConfirm(ctx, 3, extEditTestType, "request --method=DELETE --path=/logs-app")

			require.Equal(t, aictx.Deny, got.Decision)
			require.Empty(t, persistedCommands(stub), "an invalid edit must not persist any grant")
		})
	}
}

func TestParseApprovalResponseValidatesClassifiedEdits(t *testing.T) {
	expected := []ApprovalItem{{
		Type: "esverify", AssetID: 3, Command: "request --method=DELETE --path=/logs-app",
		Action: "delete", Resource: "logs-app", RememberPattern: "delete:logs-app",
	}}
	respond := func(command string) (ParsedApprovalResponse, error) {
		edited := expected[0]
		edited.Command = command
		return ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "allowAll", EditedItems: []ApprovalItem{edited}}, expected)
	}

	parsed, err := respond("delete:logs-*")
	require.NoError(t, err)
	require.Len(t, parsed.EditedItems, 1)
	require.Equal(t, "delete:logs-*", parsed.EditedItems[0].Command)

	parsed, err = respond("delete:logs-app")
	require.NoError(t, err)
	require.Equal(t, ApprovalAllowAll, parsed.Decision)
	require.Empty(t, parsed.EditedItems, "echoing the pre-filled tail back is not an edit")

	for _, bad := range []string{"get:logs-app", ":logs-app", "delete", "delete:["} {
		parsed, err = respond(bad)
		require.Error(t, err, bad)
		require.Equal(t, ApprovalDeny, parsed.Decision, bad)
	}
}
