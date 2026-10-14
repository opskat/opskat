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
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// setupGenericCommandPermission 注册一个命令方式的自定义类型与一台引用它的资产（id 1）。
func setupGenericCommandPermission(t *testing.T, slug string, ct *custom_type_entity.CustomType, cmdPolicy asset_entity.CommandPolicy) context.Context {
	t.Helper()
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{ID: 1, Name: "runner", Type: asset_entity.AssetTypeGeneric, CmdPolicy: mustJSON(cmdPolicy)}
	require.NoError(t, asset.SetGenericConfig(&asset_entity.GenericConfig{CustomType: slug}))
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	registerGenericTypes(t, ct)
	return ctx
}

// 有命令模板：匹配对象是 exec 参数串，按 MatchPlainGlob 判定——与 HTTP 共用同一套逻辑，
// SSH 风格的规则（程序名 + MatchCommandRule）既不能放行也不能拦下。
func TestCheckPermission_GenericCommandWithTemplateUsesPlainGlob(t *testing.T) {
	ct := &custom_type_entity.CustomType{
		Slug: "aws-cli", ExecMode: custom_type_entity.ExecModeCommand,
		Command: &custom_type_entity.CommandConfig{Template: "aws --region {{region}} --output json"},
		Fields:  []custom_type_entity.Field{{Name: "region"}},
	}
	ctx := setupGenericCommandPermission(t, "aws-cli", ct, asset_entity.CommandPolicy{
		AllowList: []string{"s3 ls *"},
		DenyList:  []string{"s3 rm *"},
	})

	allow := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "s3 ls s3://bucket")
	assert.Equal(t, aictx.Allow, allow.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allow.DecisionSource)

	deny := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "s3 rm s3://bucket/x")
	assert.Equal(t, aictx.Deny, deny.Decision)

	confirm := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "s3 cp a b")
	assert.Equal(t, aictx.NeedConfirm, confirm.Decision)
}

func TestCheckPermission_GenericCommandWithTemplateIsNotShellParsed(t *testing.T) {
	ct := &custom_type_entity.CustomType{
		Slug: "aws-cli", ExecMode: custom_type_entity.ExecModeCommand,
		Command: &custom_type_entity.CommandConfig{Template: "aws"},
	}
	// "s3" 作为 SSH 风格的规则（程序名）本该放行 "s3 ls ..."，但命令模板存在时匹配对象
	// 是参数串、按普通 glob 判定，MatchCommandRule 语义在这里不适用。
	ctx := setupGenericCommandPermission(t, "aws-cli", ct, asset_entity.CommandPolicy{AllowList: []string{"s3"}})
	assert.Equal(t, aictx.NeedConfirm, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "s3 ls x").Decision,
		"MatchCommandRule would treat `s3` as a program name matching any arguments; the plain glob must not")
}

// 无命令模板：匹配对象是整条 shell 命令，按 SSH 同款的子命令拆分 + MatchCommandRule 判定，
// 一个规则要放行含管道的命令必须放行每一段子命令。
func TestCheckPermission_GenericCommandNoTemplateUsesShellRules(t *testing.T) {
	ct := &custom_type_entity.CustomType{Slug: "shell-box", ExecMode: custom_type_entity.ExecModeCommand, Command: &custom_type_entity.CommandConfig{}}
	ctx := setupGenericCommandPermission(t, "shell-box", ct, asset_entity.CommandPolicy{
		AllowList: []string{"ls *", "grep *"},
		DenyList:  []string{"rm *"},
	})

	allow := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "ls -la | grep foo")
	assert.Equal(t, aictx.Allow, allow.Decision, "every sub-command of the pipeline is allowed")

	deny := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "rm -rf /")
	assert.Equal(t, aictx.Deny, deny.Decision)

	confirm := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "ls -la | wc -l")
	assert.Equal(t, aictx.NeedConfirm, confirm.Decision, "the allow list does not cover the second sub-command, and no deny rule matches it either")
}

func TestCheckPermission_GenericCommandNoTemplateGrantRoundTrip(t *testing.T) {
	ct := &custom_type_entity.CustomType{Slug: "shell-box", ExecMode: custom_type_entity.ExecModeCommand, Command: &custom_type_entity.CommandConfig{}}
	ctx := setupGenericCommandPermission(t, "shell-box", ct, asset_entity.CommandPolicy{})

	stub := newStubGrantRepo()
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() { grant_repo.RegisterGrant(origGrant) })
	stub.sessions["s1"] = &grant_entity.GrantSession{ID: "s1", Status: grant_entity.GrantStatusApproved}
	stub.items["s1"] = []*grant_entity.GrantItem{
		{GrantSessionID: "s1", AssetID: 1, ToolName: "generic", Command: "ls *"},
	}
	grantCtx := aictx.WithSessionID(ctx, "s1")
	granted := CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, "ls -la")
	assert.Equal(t, aictx.Allow, granted.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, granted.DecisionSource)
	assert.Equal(t, aictx.NeedConfirm, CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, "rm -rf /").Decision)
}

// 「全部允许」与「永久允许」落下的是 NormalizeGrantPatterns 的结果：通用资产的归一化不知道
// 执行方式，整串存一条（系统主体转义通配元字符）。无模板命令方式按子命令匹配，存下的整串
// 必须同样按子命令拆开才对得上——否则批准过的复合命令下次仍要审批。
func TestCheckPermission_GenericCommandNoTemplateHonorsStoredCompoundApprovals(t *testing.T) {
	ct := &custom_type_entity.CustomType{Slug: "shell-box", ExecMode: custom_type_entity.ExecModeCommand, Command: &custom_type_entity.CommandConfig{}}

	t.Run("session grant", func(t *testing.T) {
		ctx := setupGenericCommandPermission(t, "shell-box", ct, asset_entity.CommandPolicy{})
		stub := newStubGrantRepo()
		origGrant := grant_repo.Grant()
		grant_repo.RegisterGrant(stub)
		t.Cleanup(func() { grant_repo.RegisterGrant(origGrant) })
		grantCtx := aictx.WithSessionID(ctx, "s1")

		for _, approved := range []string{"ls -la | grep foo", "cat *.log && wc -l x"} {
			SaveGrantPatternsForApproval(grantCtx, "s1", 1, "runner", ApprovalTypeFor(asset_entity.AssetTypeGeneric), approved, GrantOriginSystem)
			got := CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, approved)
			assert.Equal(t, aictx.Allow, got.Decision, "the approved command %q must be granted", approved)
		}
		assert.Equal(t, aictx.NeedConfirm, CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, "cat secret.log").Decision,
			"the escaped `*` of an approved command stays literal")
		assert.Equal(t, aictx.NeedConfirm, CheckPermission(grantCtx, asset_entity.AssetTypeGeneric, 1, "ls -la | rm -rf /").Decision)
	})

	t.Run("persisted allow rule", func(t *testing.T) {
		ctx := setupGenericCommandPermission(t, "shell-box", ct, asset_entity.CommandPolicy{AllowList: []string{"ls -la | grep foo"}})
		assert.Equal(t, aictx.Allow, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "ls -la | grep foo").Decision)
		assert.Equal(t, aictx.NeedConfirm, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "ls -la | wc -l").Decision)
	})
}

// 取值的匹配对象是 `secret:<字段名>`；exec 的命令只是恰好以 "secret:" 开头时仍是命令，必须按
// 执行方式判定——否则无模板命令方式里 `secret:x; rm -rf /` 会绕开 shell 子命令拆分与 deny 规则，
// 被 `secret:*` 这条只为取值写的 allow 规则放行。
func TestCheckPermission_GenericCommandPrefixedLikeSecretIsStillACommand(t *testing.T) {
	ct := &custom_type_entity.CustomType{Slug: "shell-box", ExecMode: custom_type_entity.ExecModeCommand, Command: &custom_type_entity.CommandConfig{},
		Fields: []custom_type_entity.Field{{Name: "token", Secret: true}}}
	ctx := setupGenericCommandPermission(t, "shell-box", ct, asset_entity.CommandPolicy{
		AllowList: []string{"secret:*"},
		DenyList:  []string{"rm *"},
	})

	got := CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "secret:x; rm -rf /")
	assert.Equal(t, aictx.Deny, got.Decision, "the rm sub-command hits the deny rule")
	assert.Equal(t, aictx.Allow, CheckPermission(ctx, asset_entity.AssetTypeGeneric, 1, "secret:token").Decision,
		"a well-formed secret subject is still judged as a value read")
}
