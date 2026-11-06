package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
)

// reviewedGenericAsset 是一台开了模型审核（mode）的 HTTP 通用资产，策略只放行 GET。
func reviewedGenericAsset(t *testing.T, mode string) context.Context {
	t.Helper()
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{ID: 1, Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric, PermissionMode: mode,
		CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{"GET *"}})}
	require.NoError(t, asset.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana"}))
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	registerGenericTypes(t, &custom_type_entity.CustomType{
		Slug: "grafana", ExecMode: custom_type_entity.ExecModeHTTP,
		HTTP:   &custom_type_entity.HTTPConfig{BaseURL: "https://g.internal"},
		Fields: []custom_type_entity.Field{{Name: "token", Secret: true}},
	})
	return ctx
}

// 取值会把明文交给调用方（来自 AI 时进入对话上下文），Design decision 9 要求由人确认：
// 模型审核判断的是"会不会破坏什么"，读一个值在它看来无害，不能据审核结果放行。
func TestCheckPermission_GenericSecretReadIsNeverModelReviewed(t *testing.T) {
	for _, c := range []struct {
		mode     string
		decision aictx.Decision
	}{
		{policyent.PermissionModeAssisted, aictx.NeedConfirm},
		{policyent.PermissionModeAutopilot, aictx.Deny},
	} {
		t.Run(c.mode, func(t *testing.T) {
			ctx := reviewedGenericAsset(t, c.mode)
			f := registerFakeReviewer(t, reviewPass)

			r := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "secret:token")

			assert.Equal(t, c.decision, r.Decision)
			assert.Empty(t, f.calls, "a secret read must not be sent to the model reviewer")
		})
	}
}

// HTTP 的策略匹配对象只有 `<方法> <路径>`，请求体、query 与调用方 header 不在里面；
// 模型审核要看到调用方实际写的整条请求，否则它是在看不到内容的情况下放行写操作。
func TestCheckPermissions_GenericHTTPReviewSeesTheRequestAsWritten(t *testing.T) {
	ctx := reviewedGenericAsset(t, policyent.PermissionModeAutopilot)
	f := registerFakeReviewer(t, reviewPass)
	raw := `POST /api/admin/users?notify=all -H 'X-Org: 1' -d '{"role":"Admin"}'`

	r := CheckPermissions(ctx, []PermissionRequest{{
		AssetType: asset_entity.AssetTypeGeneric, AssetID: 1, Command: "POST /api/admin/users", RawCommand: raw,
	}})[0]

	assert.Equal(t, aictx.Allow, r.Decision)
	require.Len(t, f.calls, 1)
	assert.Equal(t, raw, f.calls[0].Command)
}
