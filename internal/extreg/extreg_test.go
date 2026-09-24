package extreg

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/pkg/extension"
)

// --- fakes -------------------------------------------------------------------

type fakePlugin struct {
	action    string
	resource  string
	policyErr error
	callErr   error
	lastTool  string
	lastArgs  json.RawMessage
	lastAsset *extension.AssetRef
	result    string

	validationErrors []extension.ValidationError
	validateErr      error
	lastValidated    json.RawMessage
}

func (p *fakePlugin) ValidateConfig(_ context.Context, config json.RawMessage) ([]extension.ValidationError, error) {
	p.lastValidated = append(json.RawMessage(nil), config...)
	return p.validationErrors, p.validateErr
}

func (p *fakePlugin) CallTool(_ context.Context, toolName string, args json.RawMessage, asset *extension.AssetRef) (json.RawMessage, error) {
	p.lastTool = toolName
	p.lastArgs = append(json.RawMessage(nil), args...)
	p.lastAsset = asset
	if p.callErr != nil {
		return nil, p.callErr
	}
	result := p.result
	if result == "" {
		result = `{"ok":true}`
	}
	return json.RawMessage(result), nil
}

func (p *fakePlugin) CheckPolicy(_ context.Context, _ string, _ json.RawMessage) (string, string, error) {
	return p.action, p.resource, p.policyErr
}

func testManifest() *extension.Manifest {
	return &extension.Manifest{
		Name:    "acme",
		Version: "1.0.0",
		I18n:    extension.ManifestI18n{Description: "Acme object store"},
		AssetTypes: []extension.AssetTypeDef{{
			Type: "acme-store",
			I18n: extension.I18nName{Name: "Acme"},
			ConfigSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"endpoint": map[string]any{"type": "string"},
					"token":    map[string]any{"type": "string", "format": "password"},
				},
				"required": []any{"endpoint"},
			},
		}},
		Tools: []extension.ToolDef{{
			Name: "list_objects",
			I18n: extension.I18nDesc{Description: "List objects in a bucket"},
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"bucket":  map[string]any{"type": "string", "description": "Bucket name"},
					"maxKeys": map[string]any{"type": "integer"},
					"keys":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"bucket"},
			},
		}},
		Policies: extension.PoliciesDef{
			Type:    "acme",
			Actions: []string{"object.list", "object.write", "object.delete"},
			Groups: []extension.PolicyGroupDef{{
				ID:   "ext:acme:readonly",
				I18n: extension.I18nNameDesc{Name: "read only", Description: "list and read"},
				Policy: map[string]any{
					"allow_list": []any{"object.list"},
					"deny_list":  []any{"object.delete"},
				},
			}},
			Default: []string{"ext:acme:readonly"},
		},
	}
}

func registerFake(t *testing.T, plugin pluginCaller) *extension.Manifest {
	t.Helper()
	m := testManifest()
	l := loaded{name: m.Name, manifest: m, plugin: plugin}
	localized := m.Localized(func(k string) string { return k })
	require.NoError(t, register(l, helpDocument("# Acme\nProse about acme.", m.Name, localized), "acme skill"))
	t.Cleanup(func() { Unregister(m.Name) })
	return m
}

// --- registry wiring ---------------------------------------------------------

func TestRegisterMakesTypeReachableThroughTheSharedRegistries(t *testing.T) {
	registerFake(t, &fakePlugin{})

	_, ok := assettype.Get("acme-store")
	assert.True(t, ok, "extension asset type must land in the shared asset-type registry")

	exec, ok := permission.ExecutorFor("acme-store")
	require.True(t, ok, "exec must dispatch to an extension asset type like any other type")
	assert.NotNil(t, exec)

	assert.Contains(t, permission.RegisteredExecTypes(), "acme-store")

	_, ok = permission.CanonicalizeFor("acme-store")
	assert.True(t, ok, "the flag DSL must be canonicalized before the approval dialog")

	desc, ok := skills.Description("acme-store")
	require.True(t, ok, "the type must be discoverable in the prompt's skill listing")
	assert.Equal(t, "acme skill", desc)
}

func TestUnregisterRemovesEveryRegistration(t *testing.T) {
	registerFake(t, &fakePlugin{})
	Unregister("acme")

	_, ok := assettype.Get("acme-store")
	assert.False(t, ok)
	_, ok = permission.ExecutorFor("acme-store")
	assert.False(t, ok)
	_, ok = permission.HelpFor("acme-store")
	assert.False(t, ok)
	_, ok = skills.Get("acme-store")
	assert.False(t, ok)
}

func TestRegisterRefusesAssetTypeCollisionLoudly(t *testing.T) {
	registerFake(t, &fakePlugin{})

	// A second extension declaring the same asset type must be refused: asset type →
	// extension is a hard one-to-one now that exec dispatches through it.
	other := testManifest()
	other.Name = "acme-clone"
	other.Policies.Type = "acme-clone"
	other.Policies.Groups[0].ID = "ext:acme-clone:readonly"
	err := register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}},
		"help", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acme-store")

	// The refused extension must leave nothing behind.
	assert.Equal(t, "acme", mustExtensionNameOf(t, "acme-store"))
}

func mustExtensionNameOf(t *testing.T, assetType string) string {
	t.Helper()
	h, ok := assettype.Get(assetType)
	require.True(t, ok)
	owner, ok := h.(interface{ ExtensionName() string })
	require.True(t, ok)
	return owner.ExtensionName()
}

// --- help --------------------------------------------------------------------

func TestHelpRendersToolAndParameterTable(t *testing.T) {
	registerFake(t, &fakePlugin{})

	doc, ok := permission.HelpFor("acme-store")
	require.True(t, ok)

	assert.Contains(t, doc, "Prose about acme.", "the SKILL.md body must still be served")
	assert.Contains(t, doc, "### list_objects")
	assert.Contains(t, doc, "List objects in a bucket")
	// The declaration parseCommand enforces is what the model is shown.
	assert.Contains(t, doc, "| `--bucket` | string | yes | Bucket name |")
	assert.Contains(t, doc, "| `--maxKeys` | integer | no |")
	assert.Contains(t, doc, "| `--keys` | array<string> | no |")
}

// --- command parsing / execution ---------------------------------------------

func TestExecutorConvertsFlagsToTypedArguments(t *testing.T) {
	plugin := &fakePlugin{result: `{"objects":2}`}
	registerFake(t, plugin)

	exec, ok := permission.ExecutorFor("acme-store")
	require.True(t, ok)

	out, err := exec(context.Background(), &asset_entity.Asset{ID: 1, Name: "prod store", Type: "acme-store"},
		"list_objects --bucket=prod --maxKeys=10 --keys=a,b", "")
	require.NoError(t, err)
	assert.Equal(t, `{"objects":2}`, out)
	assert.Equal(t, "list_objects", plugin.lastTool)
	assert.JSONEq(t, `{"bucket":"prod","maxKeys":10,"keys":["a","b"]}`, string(plugin.lastArgs))
	// The exec target reaches the guest as the call's asset, not as an argument
	// the command had to repeat.
	assert.Equal(t, &extension.AssetRef{ID: 1, Name: "prod store", Type: "acme-store"}, plugin.lastAsset)
}

func TestCanonicalizeIsStableAndRejectsBadCommandsBeforeApproval(t *testing.T) {
	registerFake(t, &fakePlugin{})
	canon, ok := permission.CanonicalizeFor("acme-store")
	require.True(t, ok)

	asset := &asset_entity.Asset{ID: 1, Type: "acme-store"}
	a, err := canon(asset, "list_objects --maxKeys=10 --bucket=prod")
	require.NoError(t, err)
	b, err := canon(asset, "list_objects --bucket=prod --maxKeys=10")
	require.NoError(t, err)
	assert.Equal(t, a, b, "flag order must not change the policy/grant subject")

	_, err = canon(asset, "list_objects --nope=1")
	require.Error(t, err, "an undeclared flag must fail before the approval dialog")
	_, err = canon(asset, "no_such_tool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list_objects")
}

// --- policy + grant ----------------------------------------------------------

// TestExtensionToolHonoursAnApprovedGrant is the regression lock for the defect this
// change fixes: extension tool calls used to bypass CheckForAsset entirely and pop the
// confirmation dialog straight from checker.ConfirmFunc(), so "always allow" persisted a
// grant that nothing ever consulted — the user was re-prompted for the same command
// forever. Going through the registered PolicyCheckFunc puts extension calls on the same
// grant path as every built-in type.
func TestExtensionToolHonoursAnApprovedGrant(t *testing.T) {
	// object.write is declared but neither allowed nor denied by the asset's groups, so
	// the policy layer cannot decide and the outcome is determined purely by whether a
	// grant matches.
	registerFake(t, &fakePlugin{action: "object.write", resource: "prod-bucket"})
	ctx := withGrantFixture(t, 1, "acme-store")

	const command = "list_objects --bucket=prod"

	got := permission.CheckPermission(ctx, "acme-store", 1, command)
	require.Equal(t, aictx.NeedConfirm, got.Decision, "without a grant the user must be asked")

	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", "ext:acme:object.write:prod-bucket")

	got = permission.CheckPermission(ctx, "acme-store", 1, command)
	assert.Equal(t, aictx.Allow, got.Decision, "an approved grant must skip the prompt on the next identical call")
	assert.Equal(t, aictx.SourceGrantAllow, got.DecisionSource)
}

// allowAllChecker returns a CommandPolicyChecker whose confirmFunc always answers
// "allowAll" — the shortest way to drive the real HandleConfirm persistence path
// instead of pre-seeding a grant with SaveGrantPattern.
func allowAllChecker() *permission.CommandPolicyChecker {
	return permission.NewCommandPolicyChecker(func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		return permission.ApprovalResponse{Decision: "allowAll"}
	})
}

// TestExtensionAllowAllPersistsClassificationGrant is task 7's core regression lock:
// "always allow" must persist ext:<type>:<action>:<resource> — the check_policy
// classification — not the raw command text, and a later call whose command text
// differs but classifies the same must still hit it (guest ignores flag spelling/order
// here, which is exactly the point: matching never re-reads the command string).
func TestExtensionAllowAllPersistsClassificationGrant(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write", resource: "prod-bucket"})
	ctx := withGrantFixture(t, 1, "acme-store")
	checker := allowAllChecker()

	first := checker.CheckForAsset(ctx, 1, "acme-store", "list_objects --bucket=prod")
	require.Equal(t, aictx.Allow, first.Decision)
	require.Equal(t, aictx.SourceUserAllow, first.DecisionSource)

	repo := grant_repo.Grant()
	items, err := repo.ListApprovedItems(ctx, "sess-ext")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "ext:acme:object.write:prod-bucket", items[0].Command,
		"the persisted grant must be the ext:<type>:<action>:<resource> classification")

	// A later call with different argument spelling classifies identically (the fake
	// plugin ignores args), so it must hit the grant without re-prompting.
	second := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod --maxKeys=10")
	assert.Equal(t, aictx.Allow, second.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, second.DecisionSource)
}

// TestExtensionGrantDoesNotCoverADifferentResource locks the other half of
// classification matching: a grant for one resource must not silently widen to cover
// a different one just because the action and asset are the same.
func TestExtensionGrantDoesNotCoverADifferentResource(t *testing.T) {
	plugin := &fakePlugin{action: "object.write", resource: "prod-bucket"}
	registerFake(t, plugin)
	ctx := withGrantFixture(t, 1, "acme-store")
	checker := allowAllChecker()

	require.Equal(t, aictx.Allow, checker.CheckForAsset(ctx, 1, "acme-store", "list_objects --bucket=prod").Decision)

	plugin.resource = "staging-bucket"
	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.NeedConfirm, got.Decision, "a grant for one resource must not cover another")
}

// TestExtensionUndeclaredActionNeverGrants is the safety property behind the
// undeclared-action fail-closed rule (extreg.classifyCommand): even though "always
// allow" is still honored for the one call in front of the user, it must never leave
// behind a grant nothing could legitimately match on a second, silent call.
func TestExtensionUndeclaredActionNeverGrants(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.rename"}) // not in Policies.Actions
	ctx := withGrantFixture(t, 1, "acme-store")
	checker := allowAllChecker()

	result := checker.CheckForAsset(ctx, 1, "acme-store", "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, result.Decision, "the in-flight call is still honored once")

	repo := grant_repo.Grant()
	items, err := repo.ListApprovedItems(ctx, "sess-ext")
	require.NoError(t, err)
	assert.Empty(t, items, "an undeclared action must never produce a persisted grant")

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.NeedConfirm, got.Decision, "no phantom grant must silently allow the next call")
}

// TestExtensionPolicyDenyBeatsAnExistingGrant covers a deny rule added after a grant
// already exists for the same classification (e.g. the group's policy changed): deny
// must still win, matching the decision order (deny → allow → grant → confirm) every
// other permission type already follows.
func TestExtensionPolicyDenyBeatsAnExistingGrant(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.delete", resource: "prod-bucket"})
	ctx := withGrantFixture(t, 1, "acme-store")

	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", "ext:acme:object.delete:prod-bucket")

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Deny, got.Decision, "policy deny must win even when a grant matches the same classification")
}

// TestExtensionApprovalSupportsAlwaysAllow locks the other half of the same defect: the
// approval dialog for an extension command must offer "always allow", which the old
// ApprovalKindExtension explicitly refused. NormalizeGrantPatterns' whole-command
// fallback below is what a type with no ClassifyFunc gets; HandleConfirm's real
// AllowAll path for a classify-registered type (every extension type, in practice)
// bypasses it in favor of extGrantKey — see TestExtensionAllowAllPersistsClassificationGrant.
func TestExtensionApprovalSupportsAlwaysAllow(t *testing.T) {
	registerFake(t, &fakePlugin{})

	assert.Equal(t, permission.ApprovalKindSingle, permission.ApprovalKindFor("acme-store", "list_objects --bucket=prod"))
	assert.True(t, permission.SupportsGrantApproval("acme-store"))
	assert.Equal(t, []string{"list_objects --bucket=prod"},
		permission.NormalizeGrantPatterns("acme-store", "list_objects --bucket=prod", permission.GrantOriginSystem))
}

func TestExtensionPolicyDenyStopsBeforeGrantLookup(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.delete"})
	ctx := withGrantFixture(t, 1, "acme-store")

	// The asset references a group whose deny_list carries the action.
	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Deny, got.Decision)
	assert.Contains(t, got.Message, "object.delete")
}

// --- fixtures ----------------------------------------------------------------

type stubGrantRepo struct {
	items []*grant_entity.GrantItem
}

func (r *stubGrantRepo) CreateSession(context.Context, *grant_entity.GrantSession) error { return nil }
func (r *stubGrantRepo) GetSession(context.Context, string) (*grant_entity.GrantSession, error) {
	return &grant_entity.GrantSession{}, nil
}
func (r *stubGrantRepo) UpdateSessionStatus(context.Context, string, int) error { return nil }
func (r *stubGrantRepo) CreateItems(_ context.Context, items []*grant_entity.GrantItem) error {
	r.items = append(r.items, items...)
	return nil
}
func (r *stubGrantRepo) UpdateItems(context.Context, string, []*grant_entity.GrantItem) error {
	return nil
}
func (r *stubGrantRepo) ListItems(context.Context, string) ([]*grant_entity.GrantItem, error) {
	return r.items, nil
}
func (r *stubGrantRepo) ListApprovedItems(context.Context, string) ([]*grant_entity.GrantItem, error) {
	return r.items, nil
}

// withGrantFixture registers an in-memory grant repo plus a single asset and returns a
// context carrying the session id grant matching requires.
func withGrantFixture(t *testing.T, assetID int64, assetType string) context.Context {
	t.Helper()
	return withGrantFixturePolicy(t, assetID, assetType,
		&asset_entity.CommandPolicy{Groups: []string{"ext:acme:readonly"}})
}

// withGrantFixturePolicy is withGrantFixture with the asset's own command policy spelled
// out — permanent extension rules land in that same column.
func withGrantFixturePolicy(t *testing.T, assetID int64, assetType string, cp *asset_entity.CommandPolicy) context.Context {
	t.Helper()
	asset := &asset_entity.Asset{ID: assetID, Name: "acme-1", Type: assetType}
	require.NoError(t, asset.SetCommandPolicy(cp))

	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(&stubGrantRepo{})
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), assetID).Return(asset, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		grant_repo.RegisterGrant(origGrant)
		asset_repo.RegisterAsset(origAsset)
	})
	return aictx.WithSessionID(context.Background(), "sess-ext")
}
