package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

func powershellSSHAsset(id int64, mode string, pol *asset_entity.CommandPolicy) *asset_entity.Asset {
	a := &asset_entity.Asset{
		ID:             id,
		Name:           "win-ssh",
		Type:           asset_entity.AssetTypeSSH,
		PermissionMode: mode,
	}
	_ = a.SetSSHConfig(&asset_entity.SSHConfig{
		Host:     "10.0.0.21",
		Port:     22,
		Username: "admin",
		AuthType: asset_entity.AuthTypePassword,
		Shell:    asset_entity.SSHShellPowerShell,
	})
	if pol != nil {
		a.CmdPolicy = mustJSON(*pol)
	}
	return a
}

func TestPowerShellSSHPermissionAndReview(t *testing.T) {
	ctx, mockRepo, _ := setupPolicyTest(t)
	psInspect := `'--- TOP LEVEL ---'; Get-ChildItem C:\ -Directory -Force | ForEach-Object { $s=(Get-ChildItem $_.FullName -Recurse | Measure-Object Length -Sum).Sum; [pscustomobject]@{D=$_.Name;GB=[math]::Round($s/1GB,1)} }`

	t.Run("PowerShell SSH 资产在 assisted 模式下正常送审并自动放行", func(t *testing.T) {
		mockRepo.EXPECT().Find(gomock.Any(), int64(201)).Return(powershellSSHAsset(201, policyent.PermissionModeAssisted, nil), nil).AnyTimes()
		f := registerFakeReviewer(t, reviewPass)

		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 201, psInspect)
		require.Equal(t, aictx.Allow, r.Decision)
		require.Equal(t, aictx.SourceAssistedAllow, r.DecisionSource)
		require.NotNil(t, r.Review)
		require.Equal(t, aictx.ReviewPass, r.Review.Outcome)
		require.Equal(t, []ReviewRequest{{
			AssetType: asset_entity.AssetTypeSSH,
			Command:   psInspect,
			Syntax:    SyntaxPowerShell,
		}}, f.calls)
	})

	t.Run("PowerShell SSH 资产的 deny 规则可拦截嵌套在 ForEach-Object 里的删除命令", func(t *testing.T) {
		pol := &asset_entity.CommandPolicy{DenyList: []string{"Remove-Item *"}}
		mockRepo.EXPECT().Find(gomock.Any(), int64(202)).Return(powershellSSHAsset(202, policyent.PermissionModeAssisted, pol), nil).AnyTimes()
		f := registerFakeReviewer(t, reviewPass)

		cmd := `Get-ChildItem C:\tmp | ForEach-Object { Remove-Item $_.FullName -Force }`
		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 202, cmd)
		require.Equal(t, aictx.Deny, r.Decision)
		require.Equal(t, aictx.SourcePolicyDeny, r.DecisionSource)
		require.Empty(t, f.calls)
	})

	t.Run("PowerShell 编码混淆命令被 pwshdecode 拦下，不送审", func(t *testing.T) {
		mockRepo.EXPECT().Find(gomock.Any(), int64(203)).Return(powershellSSHAsset(203, policyent.PermissionModeAutopilot, nil), nil).AnyTimes()
		f := registerFakeReviewer(t, reviewPass)

		cmd := `[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('aWQ=')) | iex`
		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 203, cmd)
		require.Equal(t, aictx.Deny, r.Decision)
		require.Equal(t, aictx.SourceAutopilotDeny, r.DecisionSource)
		require.NotNil(t, r.Review)
		require.Equal(t, aictx.ReviewFail, r.Review.Outcome)
		require.Equal(t, aictx.ReviewReasonUndecodable, r.Review.Reason)
		require.Empty(t, f.calls)
	})
}

// PowerShell 绕过写法：本地拆分器若少拆出一条子命令，剩下的子命令全都命中 allow 规则或 grant，
// 整条命令就会不经模型、不经人工直接执行。每条都已在 Windows 上用 PowerShell 自身的
// Parser::ParseInput 核对过：它们实际执行的不止 Get-ChildItem / Get-Item。
var psBypassCommands = map[string]string{
	"词中的 # 不是注释":       `Get-ChildItem a#; Remove-Item -Recurse C:\data`,
	"Unicode 弯引号闭合字符串": "Get-ChildItem '\u2019; Remove-Item -Recurse C:\\data; '\u2019",
	"变量上的方法调用":         `$f = Get-Item C:\data\x.db; $f.Delete()`,
	"括号表达式上的方法调用":      `(Get-Item C:\data\x.db).Delete()`,
	"单独的 CR 也是换行":      "Get-ChildItem x\rRemove-Item -Recurse C:\\data",
}

func withPowerShellGrantSession(t *testing.T) context.Context {
	stub := newStubGrantRepo()
	orig := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)
	t.Cleanup(func() {
		if orig != nil {
			grant_repo.RegisterGrant(orig)
		}
	})
	return aictx.WithSessionID(context.Background(), "sess-ps")
}

func TestPowerShellRuleAllowCannotBeBypassed(t *testing.T) {
	_, mockRepo, _ := setupPolicyTest(t)
	ctx := withPowerShellGrantSession(t)
	pol := &asset_entity.CommandPolicy{AllowList: []string{"Get-ChildItem *", "Get-Item *", "Get-Date"}}
	mockRepo.EXPECT().Find(gomock.Any(), int64(301)).Return(powershellSSHAsset(301, policyent.PermissionModeDefault, pol), nil).AnyTimes()
	mockRepo.EXPECT().Find(gomock.Any(), int64(302)).Return(powershellSSHAsset(302, policyent.PermissionModeDefault, nil), nil).AnyTimes()
	SaveGrantPattern(ctx, "sess-ps", 302, "win-ssh", "exec", "Get-ChildItem *")
	SaveGrantPattern(ctx, "sess-ps", 302, "win-ssh", "exec", "Get-Item *")

	for name, cmd := range psBypassCommands {
		t.Run("allow 规则："+name, func(t *testing.T) {
			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 301, cmd)
			require.NotEqual(t, aictx.Allow, r.Decision, "cmd=%s matched=%s", cmd, r.MatchedPattern)
		})
		t.Run("grant："+name, func(t *testing.T) {
			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 302, cmd)
			require.NotEqual(t, aictx.Allow, r.Decision, "cmd=%s matched=%s", cmd, r.MatchedPattern)
		})
	}

	t.Run("词法无歧义的简单命令照常按规则 / grant 放行，命令名不区分大小写", func(t *testing.T) {
		for _, cmd := range []string{
			`Get-ChildItem C:\logs -Recurse`,
			`get-childitem C:\logs`,
			`Get-Date; Get-ChildItem 'C:\Program Files' | Get-Item`,
		} {
			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 301, cmd)
			require.Equal(t, aictx.Allow, r.Decision, "cmd=%s", cmd)
			require.Equal(t, aictx.SourcePolicyAllow, r.DecisionSource, "cmd=%s", cmd)
		}
		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 302, `Get-ChildItem C:\logs`)
		require.Equal(t, aictx.Allow, r.Decision)
		require.Equal(t, aictx.SourceGrantAllow, r.DecisionSource)
	})
}

func TestPowerShellRulesTreatBackslashAsPathSeparator(t *testing.T) {
	_, mockRepo, _ := setupPolicyTest(t)
	ctx := withPowerShellGrantSession(t)
	pol := &asset_entity.CommandPolicy{AllowList: []string{`Get-ChildItem C:\logs\*`}}
	mockRepo.EXPECT().Find(gomock.Any(), int64(305)).Return(powershellSSHAsset(305, policyent.PermissionModeDefault, pol), nil).AnyTimes()
	mockRepo.EXPECT().Find(gomock.Any(), int64(306)).Return(powershellSSHAsset(306, policyent.PermissionModeDefault, nil), nil).AnyTimes()

	t.Run("审批时「始终允许」存下的 pattern 能再次命中同一条命令", func(t *testing.T) {
		cmd := `Get-ChildItem 'C:\Program Files' | Select-Object -First 3`
		SaveGrantPatternsForApproval(ctx, "sess-ps", 306, "win-ssh", "exec", cmd, GrantOriginSystem)
		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 306, cmd)
		require.Equal(t, aictx.Allow, r.Decision)
		require.Equal(t, aictx.SourceGrantAllow, r.DecisionSource)
	})

	t.Run("路径里的 * 不跨目录", func(t *testing.T) {
		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 305, `Get-ChildItem C:\logs\app.log`)
		require.Equal(t, aictx.Allow, r.Decision)
		for _, cmd := range []string{`Get-ChildItem C:\logs\..\..\Windows\System32\config`, `Get-ChildItem C:\logs/../../Windows`} {
			r = CheckPermission(ctx, asset_entity.AssetTypeSSH, 305, cmd)
			require.NotEqual(t, aictx.Allow, r.Decision, cmd)
		}
	})
}

func TestPowerShellComplexCommandGoesToReviewNotRules(t *testing.T) {
	ctx, mockRepo, _ := setupPolicyTest(t)
	pol := &asset_entity.CommandPolicy{AllowList: []string{"Get-Item *"}}
	mockRepo.EXPECT().Find(gomock.Any(), int64(303)).Return(powershellSSHAsset(303, policyent.PermissionModeAssisted, pol), nil).AnyTimes()
	f := registerFakeReviewer(t, reviewReject)

	cmd := psBypassCommands["括号表达式上的方法调用"]
	r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 303, cmd)
	require.Equal(t, aictx.NeedConfirm, r.Decision)
	require.Equal(t, []ReviewRequest{{AssetType: asset_entity.AssetTypeSSH, Command: cmd, Syntax: SyntaxPowerShell}}, f.calls)
}

func TestPowerShellDenyIgnoresCaseAndBuiltinAliases(t *testing.T) {
	ctx, mockRepo, _ := setupPolicyTest(t)
	pol := &asset_entity.CommandPolicy{DenyList: []string{"Remove-Item *"}}
	mockRepo.EXPECT().Find(gomock.Any(), int64(304)).Return(powershellSSHAsset(304, policyent.PermissionModeDefault, pol), nil).AnyTimes()

	for _, cmd := range []string{
		`remove-item C:\data`,
		`REMOVE-ITEM C:\data`,
		`rm -r C:\data`,
		`del C:\data\x.db`,
		`ri C:\data`,
		`Get-ChildItem C:\tmp | ForEach-Object { rm $_.FullName -Force }`,
	} {
		r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 304, cmd)
		require.Equal(t, aictx.Deny, r.Decision, "cmd=%s", cmd)
		require.Equal(t, aictx.SourcePolicyDeny, r.DecisionSource, "cmd=%s", cmd)
	}
}
