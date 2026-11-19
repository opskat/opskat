package permission

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
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
