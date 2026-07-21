package group_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func setupTest(t *testing.T) (context.Context, *mock_group_repo.MockGroupRepo, *mock_asset_repo.MockAssetRepo) {
	mockCtrl := gomock.NewController(t)
	t.Cleanup(func() { mockCtrl.Finish() })
	ctx := dbutil.WithTransactionRunner(context.Background(), func(ctx context.Context, fn func(context.Context) error) error {
		return fn(ctx)
	})
	mockGroupRepo := mock_group_repo.NewMockGroupRepo(mockCtrl)
	mockAssetRepo := mock_asset_repo.NewMockAssetRepo(mockCtrl)
	group_repo.RegisterGroup(mockGroupRepo)
	asset_repo.RegisterAsset(mockAssetRepo)
	return ctx, mockGroupRepo, mockAssetRepo
}

func TestGroupSvc_Create(t *testing.T) {
	ctx, mockGroupRepo, _ := setupTest(t)

	convey.Convey("创建分组", t, func() {
		convey.Convey("合法分组创建成功，设置时间戳", func() {
			group := &group_entity.Group{Name: "生产环境"}
			mockGroupRepo.EXPECT().Create(gomock.Any(), group).Return(nil)

			err := Group().Create(ctx, group)
			assert.NoError(t, err)
			assert.Greater(t, group.Createtime, int64(0))
			assert.Greater(t, group.Updatetime, int64(0))
		})

		convey.Convey("名称为空时 Validate 拦截，不调用 repo.Create", func() {
			group := &group_entity.Group{Name: ""}

			err := Group().Create(ctx, group)
			assert.Error(t, err)
		})
	})
}

func TestGroupSvc_Update(t *testing.T) {
	ctx, mockGroupRepo, _ := setupTest(t)

	convey.Convey("更新分组", t, func() {
		convey.Convey("合法更新成功，设置 updatetime", func() {
			group := &group_entity.Group{ID: 1, Name: "测试分组"}
			mockGroupRepo.EXPECT().Update(gomock.Any(), group).Return(nil)

			err := Group().Update(ctx, group)
			assert.NoError(t, err)
			assert.Greater(t, group.Updatetime, int64(0))
		})

		convey.Convey("名称为空时 Validate 拦截，不调用 repo.Update", func() {
			group := &group_entity.Group{ID: 1, Name: ""}

			err := Group().Update(ctx, group)
			assert.Error(t, err)
		})
	})
}

func TestGroupSvc_Rename(t *testing.T) {
	ctx, mockGroupRepo, _ := setupTest(t)

	convey.Convey("重命名分组", t, func() {
		convey.Convey("只更新名称字段，避免覆盖图标/描述/策略", func() {
			mockGroupRepo.EXPECT().UpdateName(gomock.Any(), int64(1), "新名称").Return(nil)

			err := Group().Rename(ctx, 1, "新名称")
			assert.NoError(t, err)
		})

		convey.Convey("名称为空时 Validate 拦截，不调用 repo.UpdateName", func() {
			err := Group().Rename(ctx, 1, "")
			assert.Error(t, err)
		})
	})
}

func TestGroupSvc_Delete(t *testing.T) {
	ctx, mockGroupRepo, mockAssetRepo := setupTest(t)

	convey.Convey("删除分组", t, func() {
		convey.Convey("deleteAssets=false 时，资产移到未分组（MoveToGroup）", func() {
			group := &group_entity.Group{ID: 10, ParentID: 0}
			mockGroupRepo.EXPECT().Find(gomock.Any(), int64(10)).Return(group, nil)
			mockGroupRepo.EXPECT().ReparentChildren(gomock.Any(), int64(10), int64(0)).Return(nil)
			mockAssetRepo.EXPECT().MoveToGroup(gomock.Any(), int64(10), int64(0)).Return(nil)
			mockGroupRepo.EXPECT().Delete(gomock.Any(), int64(10)).Return(nil)

			err := Group().Delete(ctx, 10, false)
			assert.NoError(t, err)
		})

		convey.Convey("deleteAssets=true 时，删除分组下资产（DeleteByGroupID）", func() {
			group := &group_entity.Group{ID: 20, ParentID: 5}
			mockGroupRepo.EXPECT().Find(gomock.Any(), int64(20)).Return(group, nil)
			mockGroupRepo.EXPECT().ReparentChildren(gomock.Any(), int64(20), int64(5)).Return(nil)
			// 删之前先列一次：被删资产的 id 要用来断开在用连接（见
			// TestGroupSvc_Delete_ClosesDeletedAssetConnections）。
			mockAssetRepo.EXPECT().List(gomock.Any(), asset_repo.ListOptions{GroupID: 20, ExactGroupID: true}).
				Return(nil, nil)
			mockAssetRepo.EXPECT().DeleteByGroupID(gomock.Any(), int64(20)).Return(nil)
			mockGroupRepo.EXPECT().Delete(gomock.Any(), int64(20)).Return(nil)

			err := Group().Delete(ctx, 20, true)
			assert.NoError(t, err)
		})
	})
}

// TestGroupSvc_Delete_RunsInTransaction 钉住 Delete 的多步写操作在同一个事务里。
//
// Delete 要依次做三件写操作：子分组改挂父级、组内资产删除或移到未分组、删除分组本身。
// 不包事务时任何一步失败都会把分组树留在中间态——最典型的是资产已经 MoveToGroup(0)
// 但分组没删掉，用户看到一个空分组而资产全跑到未分组去了，且没有任何提示。同文件的
// Reorder 已经用 dbutil.WithTransaction，Delete 漏了。
func TestGroupSvc_Delete_RunsInTransaction(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	t.Cleanup(func() { mockCtrl.Finish() })
	mockGroupRepo := mock_group_repo.NewMockGroupRepo(mockCtrl)
	mockAssetRepo := mock_asset_repo.NewMockAssetRepo(mockCtrl)
	group_repo.RegisterGroup(mockGroupRepo)
	asset_repo.RegisterAsset(mockAssetRepo)

	inTx := false
	txCalls := 0
	ctx := dbutil.WithTransactionRunner(context.Background(), func(ctx context.Context, fn func(context.Context) error) error {
		txCalls++
		inTx = true
		defer func() { inTx = false }()
		return fn(ctx)
	})

	convey.Convey("删除分组的每一步都在同一个事务内，末步失败时错误原样冒出", t, func() {
		deleteErr := errors.New("delete group failed")
		group := &group_entity.Group{ID: 10, ParentID: 0}

		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(10)).
			DoAndReturn(func(context.Context, int64) (*group_entity.Group, error) {
				assert.True(t, inTx, "Find 必须在事务内：读到的父级 ID 决定后面怎么改挂")
				return group, nil
			})
		mockGroupRepo.EXPECT().ReparentChildren(gomock.Any(), int64(10), int64(0)).
			DoAndReturn(func(context.Context, int64, int64) error {
				assert.True(t, inTx, "ReparentChildren 必须在事务内")
				return nil
			})
		mockAssetRepo.EXPECT().MoveToGroup(gomock.Any(), int64(10), int64(0)).
			DoAndReturn(func(context.Context, int64, int64) error {
				assert.True(t, inTx, "MoveToGroup 必须在事务内")
				return nil
			})
		mockGroupRepo.EXPECT().Delete(gomock.Any(), int64(10)).
			DoAndReturn(func(context.Context, int64) error {
				assert.True(t, inTx, "Delete 必须在事务内")
				return deleteErr
			})

		err := Group().Delete(ctx, 10, false)
		assert.ErrorIs(t, err, deleteErr)
		assert.Equal(t, 1, txCalls, "三步写操作必须共用一个事务，而不是各开一个")
	})
}

// TestGroupSvc_Delete_ClosesDeletedAssetConnections 钉住"连组带资产一起删"也断连。
//
// asset_svc.Delete 会广播 assetconn.CloseAsset，但 deleteAssets=true 这条路走的是
// asset_repo.DeleteByGroupID —— 直接落到仓储，绕过了 asset_svc。不补这一段的话，
// 从界面上删掉一个含资产的分组，那些资产的 SSH 终端 / RDP 会话 / 连接池条目会全部
// 留着连一个已经不存在的资产。
//
// 广播必须在事务**提交之后**：事务回滚时资产还在，连接不该被关掉。
func TestGroupSvc_Delete_ClosesDeletedAssetConnections(t *testing.T) {
	ctx, mockGroupRepo, mockAssetRepo := setupTest(t)

	var closed []int64
	assetconn.Register("group-delete-test", func(_ context.Context, assetID int64) error {
		closed = append(closed, assetID)
		return nil
	})
	t.Cleanup(func() { assetconn.UnregisterForTest("group-delete-test") })

	convey.Convey("deleteAssets=true 时，被删资产的连接逐个断开", t, func() {
		closed = nil
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(30)).
			Return(&group_entity.Group{ID: 30, ParentID: 0}, nil)
		mockGroupRepo.EXPECT().ReparentChildren(gomock.Any(), int64(30), int64(0)).Return(nil)
		mockAssetRepo.EXPECT().List(gomock.Any(), asset_repo.ListOptions{GroupID: 30, ExactGroupID: true}).
			Return([]*asset_entity.Asset{{ID: 4}, {ID: 5}}, nil)
		mockAssetRepo.EXPECT().DeleteByGroupID(gomock.Any(), int64(30)).Return(nil)
		mockGroupRepo.EXPECT().Delete(gomock.Any(), int64(30)).Return(nil)

		assert.NoError(t, Group().Delete(ctx, 30, true))
		assert.Equal(t, []int64{4, 5}, closed)
	})

	convey.Convey("deleteAssets=false 时资产只是移到未分组，连接不能断", t, func() {
		closed = nil
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(31)).
			Return(&group_entity.Group{ID: 31, ParentID: 0}, nil)
		mockGroupRepo.EXPECT().ReparentChildren(gomock.Any(), int64(31), int64(0)).Return(nil)
		mockAssetRepo.EXPECT().MoveToGroup(gomock.Any(), int64(31), int64(0)).Return(nil)
		mockGroupRepo.EXPECT().Delete(gomock.Any(), int64(31)).Return(nil)

		assert.NoError(t, Group().Delete(ctx, 31, false))
		assert.Empty(t, closed)
	})

	convey.Convey("事务失败时不断连：资产还在，连接不该被关掉", t, func() {
		closed = nil
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(32)).
			Return(&group_entity.Group{ID: 32, ParentID: 0}, nil)
		mockGroupRepo.EXPECT().ReparentChildren(gomock.Any(), int64(32), int64(0)).Return(nil)
		mockAssetRepo.EXPECT().List(gomock.Any(), asset_repo.ListOptions{GroupID: 32, ExactGroupID: true}).
			Return([]*asset_entity.Asset{{ID: 6}}, nil)
		mockAssetRepo.EXPECT().DeleteByGroupID(gomock.Any(), int64(32)).Return(nil)
		mockGroupRepo.EXPECT().Delete(gomock.Any(), int64(32)).Return(errors.New("boom"))

		assert.Error(t, Group().Delete(ctx, 32, true))
		assert.Empty(t, closed)
	})
}

func TestGroupSvc_Get(t *testing.T) {
	ctx, mockGroupRepo, _ := setupTest(t)

	convey.Convey("获取分组", t, func() {
		convey.Convey("委托给 repo.Find，返回对应分组", func() {
			expected := &group_entity.Group{ID: 1, Name: "运维组"}
			mockGroupRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(expected, nil)

			got, err := Group().Get(ctx, 1)
			assert.NoError(t, err)
			assert.Equal(t, expected.Name, got.Name)
		})
	})
}

func TestGroupSvc_List(t *testing.T) {
	ctx, mockGroupRepo, _ := setupTest(t)

	convey.Convey("列出分组", t, func() {
		convey.Convey("委托给 repo.List，返回分组列表", func() {
			expected := []*group_entity.Group{
				{ID: 1, Name: "生产环境"},
				{ID: 2, Name: "测试环境"},
			}
			mockGroupRepo.EXPECT().List(gomock.Any()).Return(expected, nil)

			got, err := Group().List(ctx)
			assert.NoError(t, err)
			assert.Len(t, got, 2)
		})
	})
}

func TestGroupSvc_Move(t *testing.T) {
	convey.Convey("Move：同父级分组存在重复 sort_order 时，下移只移动一位", t, func() {
		ctx, mockGroupRepo, _ := setupTest(t)
		moving := &group_entity.Group{ID: 1, ParentID: 0, SortOrder: 0}
		all := []*group_entity.Group{
			moving,
			{ID: 2, ParentID: 0, SortOrder: 0},
			{ID: 3, ParentID: 0, SortOrder: 0},
		}
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(moving, nil)
		mockGroupRepo.EXPECT().List(gomock.Any()).Return(all, nil)
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(2), 10).Return(nil)
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(1), 20).Return(nil)
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(3), 30).Return(nil)

		err := Group().Move(ctx, 1, "down")
		assert.NoError(t, err)
	})
}

func TestGroupSvc_Reorder(t *testing.T) {
	convey.Convey("Reorder：同父级排序", t, func() {
		ctx, mockGroupRepo, _ := setupTest(t)
		moving := &group_entity.Group{ID: 3, ParentID: 0, SortOrder: 30}
		all := []*group_entity.Group{
			{ID: 1, ParentID: 0, SortOrder: 10},
			{ID: 2, ParentID: 0, SortOrder: 20},
			moving,
		}
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(moving, nil)
		mockGroupRepo.EXPECT().List(gomock.Any()).Return(all, nil)
		// 把 3 拖到 1 之前 → [3, 1, 2]：3→10, 1→20, 2→30
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(3), 10).Return(nil)
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(1), 20).Return(nil)
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(2), 30).Return(nil)

		err := Group().Reorder(ctx, 3, 0, 1)
		assert.NoError(t, err)
	})

	convey.Convey("Reorder：改父级", t, func() {
		ctx, mockGroupRepo, _ := setupTest(t)
		moving := &group_entity.Group{ID: 5, ParentID: 0, SortOrder: 10}
		all := []*group_entity.Group{
			moving,
			{ID: 10, ParentID: 0, SortOrder: 20},
			{ID: 11, ParentID: 10, SortOrder: 10},
		}
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(5)).Return(moving, nil)
		mockGroupRepo.EXPECT().List(gomock.Any()).Return(all, nil)
		// 拖到 ID=10 下，beforeID=0（末尾）
		mockGroupRepo.EXPECT().UpdateParentID(gomock.Any(), int64(5), int64(10)).Return(nil)
		// 目标父级 10 下兄弟（不含 5）：[11]；插入 5 在末尾 → [11, 5]
		// 11: sort_order 已经是 10，跳过；5: 写 20
		mockGroupRepo.EXPECT().UpdateSortOrder(gomock.Any(), int64(5), 20).Return(nil)

		err := Group().Reorder(ctx, 5, 10, 0)
		assert.NoError(t, err)
	})

	convey.Convey("Reorder：拖到自身下被拒", t, func() {
		ctx, _, _ := setupTest(t)
		err := Group().Reorder(ctx, 7, 7, 0)
		assert.Error(t, err)
	})

	convey.Convey("Reorder：拖到自己子孙下成环被拒", t, func() {
		ctx, mockGroupRepo, _ := setupTest(t)
		moving := &group_entity.Group{ID: 1, ParentID: 0}
		all := []*group_entity.Group{
			moving,
			{ID: 2, ParentID: 1}, // 2 是 1 的子
			{ID: 3, ParentID: 2}, // 3 是 1 的孙
		}
		mockGroupRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(moving, nil)
		mockGroupRepo.EXPECT().List(gomock.Any()).Return(all, nil)
		// 尝试把 1 拖到 3 下 → 应被拒绝
		err := Group().Reorder(ctx, 1, 3, 0)
		assert.Error(t, err)
	})
}
