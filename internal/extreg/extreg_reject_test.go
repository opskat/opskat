package extreg

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// A tool refusing a call's arguments (the SDK's RejectArgs) is a decision the host
// must honor as a deny — before any rule, grant or approval dialog — while a guest
// that fails to classify still fails closed to the user's approval.

// countingChecker is a checker whose approval dialog only counts how often it was
// shown, and approves.
func countingChecker(shown *int) *permission.CommandPolicyChecker {
	return permission.NewCommandPolicyChecker(func(context.Context, string, []permission.ApprovalItem) permission.ApprovalResponse {
		*shown++
		return permission.ApprovalResponse{Decision: "allowAll"}
	})
}

func TestExtensionArgsRejectionIsDeniedWithoutAsking(t *testing.T) {
	const reason = `path "http://evil/x" must not name a host`
	registerFake(t, &fakePlugin{policyErr: &extension.ArgsRejectedError{Reason: reason}})
	// Neither an allow rule covering every action nor a saved grant may lift it.
	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"object.list", "object.write", "object.delete"},
	})
	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", "ext:acme:object.write")

	shown := 0
	got := countingChecker(&shown).CheckForAsset(ctx, 1, "acme-store", "list_objects --bucket=prod")

	assert.Equal(t, aictx.Deny, got.Decision)
	assert.Equal(t, 0, shown, "a call the tool refuses must never raise an approval dialog")
	assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
	assert.Contains(t, got.Message, reason, "the caller and the audit row see the tool's own reason")
	assert.Contains(t, got.Message, "list_objects")
}

func TestExtensionClassificationFaultStillAsks(t *testing.T) {
	registerFake(t, &fakePlugin{policyErr: errors.New("wasm: unreachable")})
	ctx := withGrantFixture(t, 1, "acme-store")

	shown := 0
	got := countingChecker(&shown).CheckForAsset(ctx, 1, "acme-store", "list_objects --bucket=prod")

	assert.Equal(t, 1, shown, "a guest that cannot classify is asked about, not denied")
	require.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, aictx.SourceUserAllow, got.DecisionSource)
}

// The V36 path end to end: a real guest's RejectArgs through check_policy, the
// host decode and the permission check the unified exec runs.
func TestExtensionArgsRejectionFromARealGuest(t *testing.T) {
	ext := extensiontest.LoadFixture(t)
	require.NoError(t, attachExtension(ext))
	t.Cleanup(func() { detach(ext.Name) })
	ctx := withGrantFixturePolicy(t, 1, "fixture", &asset_entity.CommandPolicy{})

	shown := 0
	checker := countingChecker(&shown)
	got := checker.CheckForAsset(ctx, 1, "fixture", "reject_host --resource=http://evil/x")
	assert.Equal(t, aictx.Deny, got.Decision)
	assert.Contains(t, got.Message, `resource "http://evil/x" must not name a host`)
	assert.Equal(t, 0, shown)

	got = checker.CheckForAsset(ctx, 1, "fixture", "reject_host --resource=a")
	assert.Equal(t, aictx.Allow, got.Decision, "accepted arguments are judged as always — here, asked and approved")
	assert.Equal(t, 1, shown)
}

// recordingReviewer counts the commands handed to the reviewer port.
type recordingReviewer struct{ calls int }

func (r *recordingReviewer) ReviewCommands(_ context.Context, reqs []permission.ReviewRequest) []aictx.ReviewInfo {
	r.calls += len(reqs)
	out := make([]aictx.ReviewInfo, len(reqs))
	for i := range out {
		out[i] = aictx.ReviewInfo{Outcome: aictx.ReviewPass}
	}
	return out
}

// A command the guest cannot classify (check_policy errors, or answers an action the
// tool never declared) has no action or resource for a rule or the reviewer to judge:
// only a person can. Assisted asks, Autopilot denies, the reviewer is never called.
func TestExtensionClassificationFaultIsNeverReviewed(t *testing.T) {
	faults := map[string]*fakePlugin{
		"check_policy errors": {policyErr: errors.New("wasm: unreachable")},
		"undeclared action":   {action: "object.nuke"},
		"action with a colon": {action: "object.list:prod"},
		"empty action":        {action: ""},
	}
	for name, fake := range faults {
		for _, mode := range []string{policyent.PermissionModeAssisted, policyent.PermissionModeAutopilot} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				registerFake(t, fake)
				rev := &recordingReviewer{}
				permission.SetReviewer(rev)
				t.Cleanup(func() { permission.SetReviewer(nil) })
				ctx := withGrantFixtureAsset(t, &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store", PermissionMode: mode}, nil)

				got := permission.CheckPermissions(ctx, []permission.PermissionRequest{
					{AssetType: "acme-store", AssetID: 1, Command: "list_objects --bucket=prod"},
				})[0]

				assert.Equal(t, 0, rev.calls, "an unclassifiable command must not reach the reviewer")
				if mode == policyent.PermissionModeAutopilot {
					assert.Equal(t, aictx.Deny, got.Decision)
					assert.Equal(t, aictx.SourceAutopilotDeny, got.DecisionSource)
					assert.NotEmpty(t, got.Message)
				} else {
					assert.Equal(t, aictx.NeedConfirm, got.Decision)
				}
			})
		}
	}
}

func TestExtensionClassificationFaultIsUnreviewableWithMessage(t *testing.T) {
	registerFake(t, &fakePlugin{policyErr: errors.New("wasm: unreachable")})
	ctx := withGrantFixture(t, 1, "acme-store")

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")

	assert.Equal(t, aictx.NeedConfirm, got.Decision)
	assert.True(t, got.Unreviewable)
	assert.NotEmpty(t, got.Message, "the caller is told why a person has to decide")
}
