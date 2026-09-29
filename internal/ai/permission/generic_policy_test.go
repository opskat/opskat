package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo/mock_custom_type_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

func TestMatchPlainGlob(t *testing.T) {
	tests := []struct {
		pattern, subject string
		want             bool
	}{
		{"GET *", "GET /api/dashboards/uid/abc", true},
		{"GET /api/*", "GET /api/a/b/c", true}, // * 跨 /
		{"GET /api/*", "GET /api/", true},      // * 可为空
		{"GET /api/*", "GET /apix", false},
		{"GET /api/*", "POST /api/a", false},
		{"* /api/health", "HEAD /api/health", true},
		{"GET /api/*/raw", "GET /api/a/b/raw", true},
		{"GET /api/*/raw", "GET /api/a/b/raw/x", false},
		{"GET /a?c", "GET /abc", true},
		{"GET /a?c", "GET /ac", false},
		{"GET /x", "GET /x/y", false}, // 整串匹配
		{"get *", "GET /x", false},    // 大小写敏感
		{`GET /a\*b`, "GET /a*b", true},
		{`GET /a\*b`, "GET /axxb", false},
		{"GET /a[1]", "GET /a[1]", true}, // 没有字符类
		{"GET /a[1]", "GET /a1", false},
		{`GET /a\`, `GET /a\`, true}, // 结尾孤立 \ 按字面
		{"*", "", true},
		{"", "", true},
		{"", "GET /", false},
		{"secret:*", "secret:token", true},
		{"GET /路径/*", "GET /路径/α", true},
		{"GET /?", "GET /α", true}, // ? 匹配一个字符而不是一个字节
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, MatchPlainGlob(tt.pattern, tt.subject), "MatchPlainGlob(%q, %q)", tt.pattern, tt.subject)
	}
}

// setupGenericPermission 注册一个 HTTP 方式的自定义类型 grafana 与一台 grafana 资产（id 1）。
func setupGenericPermission(t *testing.T, cmdPolicy asset_entity.CommandPolicy) context.Context {
	t.Helper()
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{ID: 1, Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric, CmdPolicy: mustJSON(cmdPolicy)}
	require.NoError(t, asset.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana"}))
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	registerGenericTypes(t, &custom_type_entity.CustomType{
		Slug: "grafana", ExecMode: custom_type_entity.ExecModeHTTP,
		HTTP: &custom_type_entity.HTTPConfig{BaseURL: "https://g.internal"},
	})
	return ctx
}

func registerGenericTypes(t *testing.T, types ...*custom_type_entity.CustomType) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mock_custom_type_repo.NewMockCustomTypeRepo(ctrl)
	repo.EXPECT().FindBySlug(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, slug string) (*custom_type_entity.CustomType, error) {
		for _, ct := range types {
			if ct.Slug == slug {
				return ct, nil
			}
		}
		return nil, gorm.ErrRecordNotFound
	}).AnyTimes()
	orig := custom_type_repo.CustomType()
	custom_type_repo.RegisterCustomType(repo)
	t.Cleanup(func() { custom_type_repo.RegisterCustomType(orig) })
}

func TestCheckPermission_GenericHTTPDecisionOrder(t *testing.T) {
	ctx := setupGenericPermission(t, asset_entity.CommandPolicy{
		AllowList: []string{"GET *", "HEAD *", "OPTIONS *", "POST /api/search"},
		DenyList:  []string{"GET /api/admin/*", "DELETE *"},
	})

	deny := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "GET /api/admin/users")
	assert.Equal(t, aictx.Deny, deny.Decision, "deny wins over the broader GET * allow")
	assert.Equal(t, aictx.SourcePolicyDeny, deny.DecisionSource)
	assert.Equal(t, "GET /api/admin/*", deny.MatchedPattern)

	allow := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "GET /api/dashboards/uid/abc")
	assert.Equal(t, aictx.Allow, allow.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allow.DecisionSource)
	assert.Equal(t, "GET *", allow.MatchedPattern)

	confirm := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "POST /api/dashboards/db")
	assert.Equal(t, aictx.NeedConfirm, confirm.Decision)

	// grant：保存格式就是匹配对象，按同一个普通 glob 匹配；策略 deny 仍优先于 grant。
	stub := newStubGrantRepo()
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() { grant_repo.RegisterGrant(origGrant) })
	stub.sessions["s1"] = &grant_entity.GrantSession{ID: "s1", Status: grant_entity.GrantStatusApproved}
	stub.items["s1"] = []*grant_entity.GrantItem{
		{GrantSessionID: "s1", AssetID: 1, ToolName: "generic", Command: "POST /api/dashboards/*"},
		{GrantSessionID: "s1", AssetID: 1, ToolName: "generic", Command: "DELETE /api/x"},
	}
	grantCtx := aictx.WithSessionID(ctx, "s1")
	granted := CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, "POST /api/dashboards/db")
	assert.Equal(t, aictx.Allow, granted.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, granted.DecisionSource)
	assert.Equal(t, "POST /api/dashboards/*", granted.MatchedPattern)
	assert.Equal(t, aictx.Deny, CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, "DELETE /api/x").Decision)
}

// HTTP 规则不经 shell 解析：SSH 风格的规则既不能放行、也不能拦下 HTTP 匹配对象。
func TestCheckPermission_GenericHTTPIsNotShellParsed(t *testing.T) {
	ctx := setupGenericPermission(t, asset_entity.CommandPolicy{AllowList: []string{"GET"}})
	assert.Equal(t, aictx.NeedConfirm, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "GET /x").Decision,
		"MatchCommandRule would treat `GET` as a program name matching any arguments; the plain glob must not")
}

func TestCheckPermission_GenericUnknownExecModeNeedsConfirm(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{ID: 1, Name: "legacy", Type: asset_entity.AssetTypeGeneric,
		CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{"*"}})}
	require.NoError(t, asset.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "legacy"}))
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	registerGenericTypes(t, &custom_type_entity.CustomType{Slug: "legacy", ExecMode: "future-mode"})

	assert.Equal(t, aictx.NeedConfirm, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "anything").Decision)
}

// 取值（`secret:<字段>`）适用于所有执行方式的通用资产，判定与类型的 exec mode 无关——
// 用一个 genericModeChecks 里都没有登记的 exec mode 证明它不经过那张按 mode 分派的表，
// 而是直接走 checkPlainGlobPolicy；新建类型没有为取值预填任何默认规则（Design decision
// 9），未配置规则的字段落到 NeedConfirm，不是意外放行。
func TestCheckPermission_GenericSecretFieldIgnoresExecModeAndHasNoDefaultAllow(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{ID: 1, Name: "cli-prod", Type: asset_entity.AssetTypeGeneric,
		CmdPolicy: mustJSON(asset_entity.CommandPolicy{
			AllowList: []string{"secret:token"},
			DenyList:  []string{"secret:admin_key"},
		})}
	require.NoError(t, asset.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "future-cli"}))
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	registerGenericTypes(t, &custom_type_entity.CustomType{Slug: "future-cli", ExecMode: "future-mode"})

	assert.Equal(t, aictx.Allow, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "secret:token").Decision,
		"an explicit allow rule for a secret match object must work even though \"future-mode\" has no exec-mode check registered")
	assert.Equal(t, aictx.Deny, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "secret:admin_key").Decision)
	assert.Equal(t, aictx.NeedConfirm, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "secret:other").Decision,
		"no rule matches secret:other and there is no default allow for secret values")
}

func TestGenericGrantPatterns(t *testing.T) {
	assert.Equal(t, []string{"GET /api/x"}, NormalizeGrantPatterns("generic", "GET /api/x", GrantOriginSystem))
	assert.Equal(t, []string{`GET /api/a\*b`}, NormalizeGrantPatterns("generic", "GET /api/a*b", GrantOriginSystem),
		"a system-derived subject is one concrete request; its glob metacharacters are literal")
	assert.Equal(t, []string{"GET /api/*"}, NormalizeGrantPatterns("generic", "GET /api/*", GrantOriginUser),
		"a user-written wildcard is the grant scope the user asked for")
	assert.Equal(t, ApprovalKindSingle, ApprovalKindFor("generic", "GET /api/x"))
	assert.True(t, TypeRulesSupported(asset_entity.AssetTypeGeneric), "opsctl policy allow must land generic rules")
}

func TestAssertAssetType_CustomTypeSlug(t *testing.T) {
	registerGenericTypes(t,
		&custom_type_entity.CustomType{Slug: "grafana", ExecMode: custom_type_entity.ExecModeHTTP},
		&custom_type_entity.CustomType{Slug: "prometheus", ExecMode: custom_type_entity.ExecModeHTTP},
	)
	grafana := &asset_entity.Asset{Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric}
	require.NoError(t, grafana.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana"}))
	web := &asset_entity.Asset{Name: "web-1", Type: asset_entity.AssetTypeSSH}

	assert.NoError(t, AssertAssetType(grafana, "grafana"))
	assert.NoError(t, AssertAssetType(grafana, asset_entity.AssetTypeGeneric))
	err := AssertAssetType(grafana, "prometheus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type=grafana")
	assert.Error(t, AssertAssetType(web, "grafana"))
	assert.Error(t, AssertAssetType(grafana, "ssh"))

	canonical, ok := CanonicalExecTypeFor("grafana")
	assert.True(t, ok, "batch `grafana:<asset>:<cmd>` prefixes must parse as a type")
	assert.Equal(t, asset_entity.AssetTypeGeneric, canonical)
	_, ok = CanonicalExecTypeFor("web-01")
	assert.False(t, ok, "an asset name is not a type")
}
