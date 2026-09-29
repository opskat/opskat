package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// docs/specs/2026-09-28-generic-asset.md「取值」/「策略、审批与审计」: get_asset_secret is
// the shared handler behind both the get_asset_secret AI tool and `opsctl secret get`.

// TestHandleGetAssetSecret_NonGenericAssetRejected locks "只对通用资产开放，不能用来读取
// 内置类型的密码等" — a built-in-typed asset must be rejected with a clear error, not
// silently ignored or dispatched into generic config parsing.
func TestHandleGetAssetSecret_NonGenericAssetRejected(t *testing.T) {
	setupGenericPutDB(t)
	require.NoError(t, asset_repo.Asset().Create(context.Background(), &asset_entity.Asset{
		Name: "web-1", Type: asset_entity.AssetTypeSSH,
	}))

	_, err := handleGetAssetSecret(context.Background(), map[string]any{"asset": "web-1", "field": "password"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generic")
}

// TestHandleGetAssetSecret_UnknownFieldListsAvailableFields locks "字段不存在时报错，并
// 列出可用的字段名".
func TestHandleGetAssetSecret_UnknownFieldListsAvailableFields(t *testing.T) {
	setupGenericPutDB(t)
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": "tok"}, "")

	_, err := handleGetAssetSecret(context.Background(), map[string]any{"asset": asset.Name, "field": "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
	assert.Contains(t, err.Error(), "host")
	assert.Contains(t, err.Error(), "token")
}

// TestHandleGetAssetSecret_MissingRequiredValueErrors locks "Missing required value ->
// error": a required field added to the type after the asset was created (Design decision
// 15) has no value on this asset and must fail loudly, not return an empty string.
func TestHandleGetAssetSecret_MissingRequiredValueErrors(t *testing.T) {
	setupGenericPutDB(t)
	asset := createGenericGrafanaAsset(t, "grafana-old", map[string]any{"host": "grafana.internal", "token": "tok"}, "")

	ct, err := custom_type_svc.CustomType().GetBySlug(context.Background(), "grafana")
	require.NoError(t, err)
	ct.Fields = append(ct.Fields, custom_type_entity.Field{Name: "org", Required: true})
	require.NoError(t, custom_type_svc.CustomType().Save(context.Background(), ct))

	_, err = handleGetAssetSecret(context.Background(), map[string]any{"asset": asset.Name, "field": "org"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "org")
}

// TestHandleGetAssetSecret_NonSecretFieldReturnsDirectlyWithoutApproval locks "非密钥字段
// 本来就在详情和 help 里可见，直接返回，不经过审批" — no PolicyChecker is installed on ctx
// at all, so a permission check on this path would fail with "permission checker not
// available" (permission.RequireCheckerOrPreapproved's fail-closed contract). Getting the
// plain value back proves the non-secret path never calls it.
func TestHandleGetAssetSecret_NonSecretFieldReturnsDirectlyWithoutApproval(t *testing.T) {
	setupGenericPutDB(t)
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": "tok"}, "")

	out, err := handleGetAssetSecret(context.Background(), map[string]any{"asset": asset.Name, "field": "host"})
	require.NoError(t, err)
	assert.Equal(t, "grafana.internal", out)
}

// TestHandleGetAssetSecret_SecretFieldDeniedNeverReturnsValue locks "密钥字段按策略判定
// ...默认需要确认" plus "审批弹窗写明明文将输出给调用方；来自 AI 时会进入对话上下文并发送给
// 模型服务商": the default policy (GET/HEAD/OPTIONS only) has no rule for secret:token, so
// this goes to confirm; when the human denies, the plaintext must never appear in the
// returned string, and the confirm dialog's match object / detail text must be correct.
func TestHandleGetAssetSecret_SecretFieldDeniedNeverReturnsValue(t *testing.T) {
	setupGenericPutDB(t)
	// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
	secret := "glsa_should_never_leak"
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": secret}, "")

	var gotItems []permission.ApprovalItem
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		gotItems = items
		return permission.ApprovalResponse{Decision: "deny"}
	})
	ctx := permission.WithPolicyChecker(context.Background(), checker)

	out, err := handleGetAssetSecret(ctx, map[string]any{"asset": asset.Name, "field": "token"})
	require.NoError(t, err, "a policy denial is reported through the result text, not a Go error")
	assert.NotContains(t, out, secret)

	require.Len(t, gotItems, 1)
	assert.Equal(t, "generic", gotItems[0].Type)
	assert.Equal(t, "secret:token", gotItems[0].Command)
	assert.Contains(t, gotItems[0].Detail, "model provider", "approval detail must warn the plaintext is sent to the model provider")
	assert.NotContains(t, gotItems[0].Detail, secret)
}

// TestHandleGetAssetSecret_SecretFieldAllowedReturnsValue locks the allow half of the same
// contract: once approved, the tool returns the actual plaintext.
func TestHandleGetAssetSecret_SecretFieldAllowedReturnsValue(t *testing.T) {
	setupGenericPutDB(t)
	// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
	secret := "glsa_approved_value"
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": secret}, "")

	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		return permission.ApprovalResponse{Decision: "allow"}
	})
	ctx := permission.WithPolicyChecker(context.Background(), checker)

	out, err := handleGetAssetSecret(ctx, map[string]any{"asset": asset.Name, "field": "token"})
	require.NoError(t, err)
	assert.Equal(t, secret, out)
}

// TestHandleGetAssetSecret_AllowRuleSkipsConfirmDialog locks "grant 保存该格式" / decision
// order deny -> allow -> grant -> confirm: an explicit allow rule for secret:token must
// short-circuit before ever calling the confirm callback (which would fail the test if
// invoked, proving there was no dialog).
func TestHandleGetAssetSecret_AllowRuleSkipsConfirmDialog(t *testing.T) {
	setupGenericPutDB(t)
	// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
	secret := "glsa_allow_rule_value"
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": secret}, "")

	asset.CmdPolicy = `{"allow_list":["GET *","HEAD *","OPTIONS *","secret:token"],"deny_list":null}`
	require.NoError(t, asset_repo.Asset().Update(context.Background(), asset))

	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		t.Fatal("an explicit allow rule must not open a confirm dialog")
		return permission.ApprovalResponse{}
	})
	ctx := permission.WithPolicyChecker(context.Background(), checker)

	out, err := handleGetAssetSecret(ctx, map[string]any{"asset": asset.Name, "field": "token"})
	require.NoError(t, err)
	assert.Equal(t, secret, out)
}

// TestHandleGetAssetSecret_PreapprovedSkipsCheckerEntirely locks the opsctl seam: after
// opsctl's own requireApproval already ran, the handler must accept permission.
// WithPreapproved and skip the checker lookup entirely (permission.
// RequireCheckerOrPreapproved's contract, shared with handleExec).
func TestHandleGetAssetSecret_PreapprovedSkipsCheckerEntirely(t *testing.T) {
	setupGenericPutDB(t)
	// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
	secret := "glsa_preapproved_value"
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": secret}, "")

	ctx := permission.WithPreapproved(context.Background())
	out, err := handleGetAssetSecret(ctx, map[string]any{"asset": asset.Name, "field": "token"})
	require.NoError(t, err)
	assert.Equal(t, secret, out)
}

// TestLookupGenericSecretField_ExportedForOpsctl locks the exported seam cmd/opsctl/
// command/secret.go needs to decide, before popping an approval dialog, whether a field
// is secret at all.
func TestLookupGenericSecretField_ExportedForOpsctl(t *testing.T) {
	setupGenericPutDB(t)
	asset := createGenericGrafanaAsset(t, "grafana-prod", map[string]any{"host": "grafana.internal", "token": "tok"}, "")

	gotAsset, field, resolved, err := LookupGenericSecretField(context.Background(), asset.Name, "token")
	require.NoError(t, err)
	assert.Equal(t, asset.ID, gotAsset.ID)
	assert.True(t, field.Secret)
	assert.Equal(t, "tok", resolved.Values["token"])
}

func TestSecretApprovalDetail_MentionsCallerAndModelProvider(t *testing.T) {
	detail := SecretApprovalDetail(context.Background())
	assert.True(t, strings.Contains(detail, "caller") || strings.Contains(detail, "调用方"))
	assert.True(t, strings.Contains(detail, "model provider") || strings.Contains(detail, "模型服务商"))
}
