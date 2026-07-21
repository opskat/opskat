package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"
)

// memAuditRepo 同步记录写入的审计行。WriteAssetChange 是同步调用（不像 AI 侧
// auditMiddleware 那样在 goroutine 里），所以不需要等待。
type memAuditRepo struct {
	logs []*audit_entity.AuditLog
}

func (m *memAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	m.logs = append(m.logs, log)
	return nil
}

func (m *memAuditRepo) List(_ context.Context, _ audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return m.logs, int64(len(m.logs)), nil
}

func (m *memAuditRepo) ListSessions(_ context.Context, _ int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

// setupAssetAudit 装好资产仓库 mock 与内存审计仓库，返回一个 ctx 已就绪的 System。
// System 直接构造而不走 New + Startup：Startup 会触发自动更新检查与 Wails 事件推送，
// 测试环境里没有 Wails runtime。
func setupAssetAudit(t *testing.T) (*System, *mock_asset_repo.MockAssetRepo, *memAuditRepo) {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	assetMock := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(assetMock)

	auditMem := &memAuditRepo{}
	origAudit := audit_repo.Audit()
	audit_repo.RegisterAudit(auditMem)

	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
		if origAudit != nil {
			audit_repo.RegisterAudit(origAudit)
		}
	})

	return &System{ctx: t.Context(), lang: "zh-cn"}, assetMock, auditMem
}

func sshAsset(t *testing.T, id int64, name string) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{ID: id, Name: name, Type: asset_entity.AssetTypeSSH, GroupID: 3}
	require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{
		Host: "10.0.0.1", Port: 22, Username: "root",
		AuthType: asset_entity.AuthTypePassword, Password: "s3cr3t-ciphertext",
	}))
	return a
}

// TestDeleteAsset_WritesDesktopAudit 钉住从界面删资产会落一条 source=desktop 的审计行，
// 且资产名要在——名字必须在删除**之前**读出来，删完再查就没了。
//
// 桌面路径此前一条审计都不写：同一台机器，AI 删有记录、CLI 删有记录、用户在界面上
// 点删除没有记录，事后追"谁删的"直接断线。
func TestDeleteAsset_WritesDesktopAudit(t *testing.T) {
	s, assetMock, auditMem := setupAssetAudit(t)
	assetMock.EXPECT().Find(gomock.Any(), int64(7)).Return(sshAsset(t, 7, "web-1"), nil)
	assetMock.EXPECT().Delete(gomock.Any(), int64(7)).Return(nil)

	require.NoError(t, s.DeleteAsset(7))

	require.Len(t, auditMem.logs, 1)
	got := auditMem.logs[0]
	assert.Equal(t, "desktop", got.Source)
	assert.Equal(t, "delete_asset", got.ToolName)
	assert.Equal(t, int64(7), got.AssetID)
	assert.Equal(t, "web-1", got.AssetName)
	assert.Equal(t, 1, got.Success)
	assert.NotContains(t, got.Request, "s3cr3t-ciphertext")
}

// TestCreateAsset_WritesDesktopAudit 新增走同一条链路。
func TestCreateAsset_WritesDesktopAudit(t *testing.T) {
	s, assetMock, auditMem := setupAssetAudit(t)
	asset := sshAsset(t, 0, "db-1")
	assetMock.EXPECT().Create(gomock.Any(), asset).Return(nil)

	require.NoError(t, s.CreateAsset(asset))

	require.Len(t, auditMem.logs, 1)
	assert.Equal(t, "desktop", auditMem.logs[0].Source)
	assert.Equal(t, "add_asset", auditMem.logs[0].ToolName)
	assert.Equal(t, 1, auditMem.logs[0].Success)
}

// TestUpdateAsset_FailureStillAudited 失败的修改同样要留痕：改坏了没保存成功，也是
// "有人动过这台机器"，只记成功等于把失败尝试从审计里抹掉。
func TestUpdateAsset_FailureStillAudited(t *testing.T) {
	s, assetMock, auditMem := setupAssetAudit(t)
	asset := sshAsset(t, 9, "web-2")
	assetMock.EXPECT().Update(gomock.Any(), asset).Return(assert.AnError)

	assert.Error(t, s.UpdateAsset(asset))

	require.Len(t, auditMem.logs, 1)
	assert.Equal(t, "update_asset", auditMem.logs[0].ToolName)
	assert.Equal(t, 0, auditMem.logs[0].Success)
	assert.NotEmpty(t, auditMem.logs[0].Error)
}

// TestDeleteGroup_AuditsCascadedAssetDeletes 钉住"连组带资产一起删"也逐条留痕。
//
// 单删一台机器有 delete_asset 审计行，但删一个含 20 台机器的分组走的是
// group_svc.Delete → asset_repo.DeleteByGroupID，一行都不落——从审计看那 20 台
// 机器凭空消失。这是 System.DeleteAsset 补上审计后暴露出来的另一面。
func TestDeleteGroup_AuditsCascadedAssetDeletes(t *testing.T) {
	s, assetMock, auditMem := setupAssetAudit(t)
	// group_svc.Delete 包在事务里，测试没有真实 DB，注入一个直通的 runner。
	s.ctx = dbutil.WithTransactionRunner(s.ctx, func(ctx context.Context, fn func(context.Context) error) error {
		return fn(ctx)
	})

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	groupMock := mock_group_repo.NewMockGroupRepo(ctrl)
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(groupMock)
	t.Cleanup(func() {
		if origGroup != nil {
			group_repo.RegisterGroup(origGroup)
		}
	})

	deleted := []*asset_entity.Asset{sshAsset(t, 11, "web-1"), sshAsset(t, 12, "web-2")}
	groupMock.EXPECT().Find(gomock.Any(), int64(3)).
		Return(&group_entity.Group{ID: 3, ParentID: 0}, nil)
	groupMock.EXPECT().ReparentChildren(gomock.Any(), int64(3), int64(0)).Return(nil)
	assetMock.EXPECT().List(gomock.Any(), asset_repo.ListOptions{GroupID: 3, ExactGroupID: true}).
		Return(deleted, nil)
	assetMock.EXPECT().DeleteByGroupID(gomock.Any(), int64(3)).Return(nil)
	groupMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)

	require.NoError(t, s.DeleteGroup(3, true))

	require.Len(t, auditMem.logs, 2)
	for i, log := range auditMem.logs {
		assert.Equal(t, "desktop", log.Source)
		assert.Equal(t, "delete_asset", log.ToolName)
		assert.Equal(t, 1, log.Success)
		assert.Equal(t, deleted[i].ID, log.AssetID)
		assert.Equal(t, deleted[i].Name, log.AssetName)
	}
}

// TestDeleteGroup_KeepingAssetsWritesNoAssetAudit 分组删掉但资产只是移到未分组，
// 资产还在，不该留下 delete_asset 的痕迹。
func TestDeleteGroup_KeepingAssetsWritesNoAssetAudit(t *testing.T) {
	s, assetMock, auditMem := setupAssetAudit(t)
	// group_svc.Delete 包在事务里，测试没有真实 DB，注入一个直通的 runner。
	s.ctx = dbutil.WithTransactionRunner(s.ctx, func(ctx context.Context, fn func(context.Context) error) error {
		return fn(ctx)
	})

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	groupMock := mock_group_repo.NewMockGroupRepo(ctrl)
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(groupMock)
	t.Cleanup(func() {
		if origGroup != nil {
			group_repo.RegisterGroup(origGroup)
		}
	})

	groupMock.EXPECT().Find(gomock.Any(), int64(4)).
		Return(&group_entity.Group{ID: 4, ParentID: 0}, nil)
	groupMock.EXPECT().ReparentChildren(gomock.Any(), int64(4), int64(0)).Return(nil)
	assetMock.EXPECT().MoveToGroup(gomock.Any(), int64(4), int64(0)).Return(nil)
	groupMock.EXPECT().Delete(gomock.Any(), int64(4)).Return(nil)

	require.NoError(t, s.DeleteGroup(4, false))
	assert.Empty(t, auditMem.logs)
}
