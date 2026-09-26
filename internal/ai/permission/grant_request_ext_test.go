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

// An extension asset only ever matches grants shaped ext:<policyType>:<action>
// [:<resource-glob>] (MatchExtensionGrant). A grant request for one — request_permission
// or the opsctl approval channel — is therefore written <action>[:<resource-glob>], validated like a
// permanent rule, and refused outright when it could never match: telling the caller
// "grant approved" for a pattern nothing will ever consult is the defect this locks.

const extGrantReqType = "esgrant-req-test"

// registerGrantRequestExtType registers an extension-like type the way extreg does:
// a policy check plus a permanent-rule sink declaring its action set.
func registerGrantRequestExtType(t *testing.T) {
	t.Helper()
	require.NoError(t, RegisterPolicyCheck(extGrantReqType,
		func(context.Context, int64, string) aictx.CheckResult {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}, nil))
	t.Cleanup(func() { UnregisterPolicyCheck(extGrantReqType) })
	require.NoError(t, RegisterExtensionRuleSink(extGrantReqType, "esgrant", []string{"read", "delete"}))
	t.Cleanup(func() { UnregisterRuleSink(extGrantReqType) })
}

// grantRequestFixture wires asset 3 (the extension type) and asset 4 (ssh) plus an
// in-memory grant repo, returning a context carrying the grant session.
func grantRequestFixture(t *testing.T) (context.Context, *stubGrantRepo) {
	t.Helper()
	ctx, mockAsset, _ := setupPolicyTest(t)
	mockAsset.EXPECT().Find(gomock.Any(), int64(3)).
		Return(&asset_entity.Asset{ID: 3, Name: "es-logs", Type: extGrantReqType}, nil).AnyTimes()
	mockAsset.EXPECT().Find(gomock.Any(), int64(4)).
		Return(&asset_entity.Asset{ID: 4, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	stub := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() { grant_repo.RegisterGrant(orig) })
	return aictx.WithSessionID(ctx, "sess-req"), stub
}

// approvingGrantChecker stands in for the desktop's grant dialog (internal/app/ai
// makeGrantRequestFunc): it records what was shown, answers with resp, and on approval
// persists exactly the way the app does — each item through
// SaveGrantPatternsForApproval under its own Type.
func approvingGrantChecker(shown *[]ApprovalItem, resp ApprovalResponse) (*CommandPolicyChecker, *bool) {
	asked := new(bool)
	checker := NewCommandPolicyChecker(nil)
	checker.SetGrantRequestFunc(func(ctx context.Context, items []ApprovalItem, _ string) (bool, []string) {
		*asked = true
		*shown = items
		parsed, err := ParseApprovalResponse(ApprovalKindGrant, resp, items)
		if err != nil || parsed.Decision != ApprovalAllow {
			return false, nil
		}
		persist, origin := items, GrantOriginSystem
		if len(parsed.EditedItems) > 0 {
			persist, origin = parsed.EditedItems, GrantOriginUser
		}
		var final []string
		for _, item := range persist {
			final = append(final, item.Command)
			SaveGrantPatternsForApproval(ctx, "sess-req", item.AssetID, item.AssetName, item.Type, item.Command, origin)
		}
		return true, final
	})
	return checker, asked
}

func persistedGrants(stub *stubGrantRepo) []string {
	out := make([]string, 0, len(stub.items["sess-req"]))
	for _, item := range stub.items["sess-req"] {
		out = append(out, item.Command)
	}
	return out
}

func TestExtensionGrantRequestPersistsRuleShapedGrantsThatMatch(t *testing.T) {
	registerGrantRequestExtType(t)
	ctx, stub := grantRequestFixture(t)
	var shown []ApprovalItem
	checker, _ := approvingGrantChecker(&shown, ApprovalResponse{Decision: "allow"})

	got := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 3, Patterns: []string{"delete:logs-*", "read"}}}, "cleanup")

	require.Equal(t, aictx.Allow, got.Decision)
	require.Equal(t, []string{"ext:esgrant:delete:logs-*", "ext:esgrant:read"}, persistedGrants(stub))
	// The dialog shows what the caller asked for, in rule syntax.
	require.Len(t, shown, 2)
	require.Equal(t, "delete:logs-*", shown[0].Command)

	_, ok := MatchExtensionGrant(ctx, 3, extGrantReqType, "esgrant", "delete", "logs-app")
	require.True(t, ok, "a delete on a matching resource must now run without a prompt")
	_, ok = MatchExtensionGrant(ctx, 3, extGrantReqType, "esgrant", "read", "anything/at/all")
	require.True(t, ok, "an action-only grant covers every resource, like an action-only rule")
	_, ok = MatchExtensionGrant(ctx, 3, extGrantReqType, "esgrant", "delete", "metrics-app")
	require.False(t, ok, "the resource glob still bounds the grant")
}

func TestExtensionGrantRequestRefusesPatternsThatCouldNeverMatch(t *testing.T) {
	for _, pattern := range []string{
		"purge:*",                          // undeclared action
		"delete:logs-[",                    // malformed glob
		"delete:",                          // empty resource glob
		"request --method=DELETE --path=*", // command-shaped
		"delete *",                         // command-shaped
	} {
		t.Run(pattern, func(t *testing.T) {
			registerGrantRequestExtType(t)
			ctx, stub := grantRequestFixture(t)
			var shown []ApprovalItem
			checker, asked := approvingGrantChecker(&shown, ApprovalResponse{Decision: "allow"})

			got := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 3, Patterns: []string{"read", pattern}}}, "why")

			require.Equal(t, aictx.Deny, got.Decision)
			require.False(t, *asked, "an invalid request must not reach the user as if it were grantable")
			require.Empty(t, persistedGrants(stub))
			require.NotContains(t, got.Message, "approved")
			require.Contains(t, got.Message, pattern)
		})
	}
}

func TestExtensionGrantRequestHoldsDialogEditsToTheSameSyntax(t *testing.T) {
	registerGrantRequestExtType(t)
	ctx, stub := grantRequestFixture(t)
	var shown []ApprovalItem
	// The user rewrites the request into something that could never match.
	resp := ApprovalResponse{Decision: "allow", EditedItems: []ApprovalItem{{
		Type: extGrantReqType, AssetID: 3, AssetName: "es-logs", Command: "delete *",
	}}}
	checker, asked := approvingGrantChecker(&shown, resp)

	got := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 3, Patterns: []string{"delete:logs-*"}}}, "why")

	require.True(t, *asked)
	require.Equal(t, aictx.Deny, got.Decision)
	require.Empty(t, persistedGrants(stub))
}

func TestExtensionGrantRequestLandsAValidDialogEditAsRules(t *testing.T) {
	registerGrantRequestExtType(t)
	ctx, stub := grantRequestFixture(t)
	var shown []ApprovalItem
	resp := ApprovalResponse{Decision: "allow", EditedItems: []ApprovalItem{{
		Type: extGrantReqType, AssetID: 3, AssetName: "es-logs", Command: "delete:*\nread",
	}}}
	checker, _ := approvingGrantChecker(&shown, resp)

	got := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 3, Patterns: []string{"delete:logs-*"}}}, "why")

	require.Equal(t, aictx.Allow, got.Decision)
	require.Equal(t, []string{"ext:esgrant:delete:*", "ext:esgrant:read"}, persistedGrants(stub))
}

// Built-in asset types keep their command-shaped grant request exactly as before.
func TestBuiltinGrantRequestIsUnchanged(t *testing.T) {
	registerGrantRequestExtType(t)
	ctx, stub := grantRequestFixture(t)
	var shown []ApprovalItem
	checker, _ := approvingGrantChecker(&shown, ApprovalResponse{Decision: "allow"})

	got := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 4, Patterns: []string{"systemctl * nginx"}}}, "why")

	require.Equal(t, aictx.Allow, got.Decision)
	require.Equal(t, []ApprovalItem{{Type: "grant", AssetID: 4, AssetName: "web-1", Command: "systemctl * nginx", Detail: "why"}}, shown)
	require.Equal(t, []string{"systemctl * nginx"}, persistedGrants(stub))
}
