package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
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

// A multi-resource item carries every resource as the extension returned it plus a
// backend-computed Remember pre-fill: the narrowest "<action>:<common-prefix>*" that
// covers all of them, else the bare action. Unedited, "always allow" persists it.
func TestMultiResourceApprovalPrefillsTheCommonPrefixRule(t *testing.T) {
	cases := []struct {
		name      string
		resources []string
		shown     []string
		want      string
	}{
		{"common prefix", []string{"logs-app", "logs-web"}, []string{"logs-app", "logs-web"}, "delete:logs-*"},
		{"prefix stops at first wildcard", []string{"logs-*", "logs-a?"}, []string{"logs-*", "logs-a?"}, "delete:logs-*"},
		{"escaped resources keep their quoting", []string{`a\[1\]-x`, `a\[1\]-y`}, []string{"a[1]-x", "a[1]-y"}, `delete:a\[1\]-*`},
		{"no common prefix falls back to the bare action", []string{"logs-app", "prod-1"}, []string{"logs-app", "prod-1"}, "delete"},
		{"star does not cross a slash so the prefix rule would not cover", []string{"logs-a/x", "logs-b/y"}, []string{"logs-a/x", "logs-b/y"}, "delete"},
		{"a whole escape pair is kept in the prefix", []string{`a\*1`, `a\*2`}, []string{"a*1", "a*2"}, `delete:a\**`},
		{"prefix never splits an escape pair", []string{`a\*1`, `a\[1`}, []string{"a*1", "a[1"}, `delete:a*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registerClassifiedExtType(t, tc.resources...)
			ctx, stub := confirmWithGrantRepo(t)

			var shown ApprovalItem
			checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
				shown = items[0]
				return ApprovalResponse{Decision: "allowAll"}
			})
			got := checker.HandleConfirm(ctx, 3, extEditTestType, "request --method=DELETE")

			require.Equal(t, aictx.Allow, got.Decision)
			require.Equal(t, "delete", shown.Action)
			require.Equal(t, tc.shown, shown.Resources)
			require.Empty(t, shown.Resource)
			require.Equal(t, tc.want, shown.RememberPattern)
			require.Equal(t, []string{"ext:esverify:" + tc.want}, persistedCommands(stub))
			for _, r := range tc.resources {
				require.True(t, policy.MatchExtensionRule(tc.want, "delete", r), "prefill must cover %q", r)
			}
		})
	}
}

// Echoing the backend's own pre-fill back (even the bare action, which a hand-typed
// edit may not be) is no edit; a hand-typed bare action is still refused.
func TestParseApprovalResponseAcceptsTheMultiResourcePrefillEcho(t *testing.T) {
	for _, prefill := range []string{"delete:logs-*", "delete"} {
		expected := []ApprovalItem{{
			Type: "esverify", AssetID: 3, Command: "request", Action: "delete",
			Resources: []string{"logs-app", "prod-1"}, RememberPattern: prefill,
		}}
		edited := expected[0]
		edited.Command = prefill
		parsed, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "allowAll", EditedItems: []ApprovalItem{edited}}, expected)
		require.NoError(t, err, prefill)
		require.Equal(t, ApprovalAllowAll, parsed.Decision)
		require.Empty(t, parsed.EditedItems, prefill)
	}
	expected := []ApprovalItem{{Type: "esverify", AssetID: 3, Command: "request", Action: "delete", Resources: []string{"a", "b"}, RememberPattern: "delete:*"}}
	edited := expected[0]
	edited.Command = "delete"
	_, err := ParseApprovalResponse(ApprovalKindSingle, ApprovalResponse{Decision: "allowAll", EditedItems: []ApprovalItem{edited}}, expected)
	require.Error(t, err)
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
