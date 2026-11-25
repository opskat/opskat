package group_svc

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/pkg/sortutil"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// GroupSvc 分组业务接口
type GroupSvc interface {
	Get(ctx context.Context, id int64) (*group_entity.Group, error)
	List(ctx context.Context) ([]*group_entity.Group, error)
	Create(ctx context.Context, group *group_entity.Group) error
	Update(ctx context.Context, group *group_entity.Group) error
	Rename(ctx context.Context, id int64, name string) error
	Delete(ctx context.Context, id int64, deleteAssets bool) ([]*asset_entity.Asset, error)
	Move(ctx context.Context, id int64, direction string) error
	Reorder(ctx context.Context, id, targetParentID, beforeID int64) error
}

type groupSvc struct{}

var defaultGroup = &groupSvc{}

// Group 获取 GroupSvc 实例
func Group() GroupSvc {
	return defaultGroup
}

func (s *groupSvc) Get(ctx context.Context, id int64) (*group_entity.Group, error) {
	return group_repo.Group().Find(ctx, id)
}

func (s *groupSvc) List(ctx context.Context) ([]*group_entity.Group, error) {
	return group_repo.Group().List(ctx)
}

func (s *groupSvc) Create(ctx context.Context, group *group_entity.Group) error {
	if err := group.Validate(); err != nil {
		return err
	}
	if group.ParentID < 0 {
		return fmt.Errorf("上级分组 ID 无效")
	}
	if group.ParentID > 0 {
		allGroups, err := group_repo.Group().List(ctx)
		if err != nil {
			return err
		}
		if err := validateGroupParent(0, group.ParentID, allGroups); err != nil {
			return err
		}
	}
	now := time.Now().Unix()
	group.Createtime = now
	group.Updatetime = now
	return group_repo.Group().Create(ctx, group)
}

func (s *groupSvc) Update(ctx context.Context, group *group_entity.Group) error {
	if err := group.Validate(); err != nil {
		return err
	}
	if group.ParentID < 0 {
		return fmt.Errorf("上级分组 ID 无效")
	}
	if group.ParentID > 0 {
		allGroups, err := group_repo.Group().List(ctx)
		if err != nil {
			return err
		}
		if err := validateGroupParent(group.ID, group.ParentID, allGroups); err != nil {
			return err
		}
	}
	group.Updatetime = time.Now().Unix()
	return group_repo.Group().Update(ctx, group)
}

func (s *groupSvc) Rename(ctx context.Context, id int64, name string) error {
	if id <= 0 {
		return fmt.Errorf("分组 ID 无效")
	}
	group := &group_entity.Group{Name: name}
	if err := group.Validate(); err != nil {
		return err
	}
	return group_repo.Group().UpdateName(ctx, id, name)
}

// Delete 删除分组
// deleteAssets: true 删除分组下的资产，false 移动到未分组
//
// 三步写操作（子分组改挂父级、处理组内资产、删除分组本身）共用一个事务：中途失败时
// 分组树不能停在中间态——例如资产已经被移到未分组、分组却还在，用户看到一个空分组，
// 且没有任何提示。与同文件 Reorder 的事务边界一致。
//
// deleteAssets=true 时还要断开被删资产的在用连接。asset_svc.DeleteByGroup 返回删除前的
// 资产信息；广播放在事务**提交之后**：回滚时资产还在，连接不该被关掉。
//
// 返回被连带删掉的资产，交给调用方逐条写审计（deleteAssets=false 时为空）。审计不在
// 这里写：审计来源（desktop / ai / opsctl）由调用方的 context 决定，服务层不该假设自己
// 是被谁调的。
func (s *groupSvc) Delete(ctx context.Context, id int64, deleteAssets bool) ([]*asset_entity.Asset, error) {
	var deletedAssets []*asset_entity.Asset

	err := dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		// 获取分组信息，用于将子分组挂到父分组
		group, err := group_repo.Group().Find(txCtx, id)
		if err != nil {
			return err
		}
		// 子分组挂到被删分组的父级
		if err := group_repo.Group().ReparentChildren(txCtx, id, group.ParentID); err != nil {
			return err
		}
		// 处理分组下的资产
		if deleteAssets {
			assets, err := asset_svc.Asset().DeleteByGroup(txCtx, id)
			if err != nil {
				return err
			}
			deletedAssets = assets
		} else {
			if err := asset_svc.Asset().MoveFromGroup(txCtx, id); err != nil {
				return err
			}
		}
		return group_repo.Group().Delete(txCtx, id)
	})
	if err != nil {
		return nil, err
	}

	for _, asset := range deletedAssets {
		assetconn.CloseAsset(ctx, asset.ID)
	}
	return deletedAssets, nil
}

// Move 移动分组排序（up/down/top）
func (s *groupSvc) Move(ctx context.Context, id int64, direction string) error {
	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return err
	}
	allGroups, err := group_repo.Group().List(ctx)
	if err != nil {
		return err
	}
	var siblings []*group_entity.Group
	for _, g := range allGroups {
		if g.ParentID == group.ParentID {
			siblings = append(siblings, g)
		}
	}
	return sortutil.MoveItem(id, direction, siblings,
		func(item *group_entity.Group) int64 { return item.ID },
		func(item *group_entity.Group) int { return item.SortOrder },
		func(itemID int64, order int) error {
			return group_repo.Group().UpdateSortOrder(ctx, itemID, order)
		},
	)
}

// validateGroupParent 校验目标上级分组是否合法：允许为 0（顶级），否则必须存在，
// 且 id > 0 时不能指向自身或自身子孙（防止成环）。
func validateGroupParent(id, targetParentID int64, allGroups []*group_entity.Group) error {
	if targetParentID == 0 {
		return nil
	}
	if targetParentID < 0 {
		return fmt.Errorf("上级分组 ID 无效")
	}
	if id > 0 && id == targetParentID {
		return fmt.Errorf("不能把分组拖到自身下")
	}
	parents := make(map[int64]int64, len(allGroups))
	for _, g := range allGroups {
		parents[g.ID] = g.ParentID
	}
	if _, exists := parents[targetParentID]; !exists {
		return fmt.Errorf("上级分组不存在: %d", targetParentID)
	}
	if id > 0 {
		visited := make(map[int64]struct{})
		cursor := targetParentID
		for cursor != 0 {
			if cursor == id {
				return fmt.Errorf("不能把分组拖到自己的子孙下")
			}
			if _, seen := visited[cursor]; seen {
				break
			}
			visited[cursor] = struct{}{}
			next, ok := parents[cursor]
			if !ok {
				break
			}
			cursor = next
		}
	}
	return nil
}

// Reorder 把 id 移动到 targetParentID 下 beforeID 之前。beforeID == 0 表示插到末尾。
// 校验：不能把分组拖进自己或自己的子孙（成环），且非 0 目标父级必须存在。
// 跨父级时同步 ParentID。重排目标父级下所有兄弟的 sort_order，等间距 (10/20/30...)。
func (s *groupSvc) Reorder(ctx context.Context, id, targetParentID, beforeID int64) error {
	if targetParentID < 0 {
		return fmt.Errorf("上级分组 ID 无效")
	}
	if id == targetParentID {
		return fmt.Errorf("不能把分组拖到自身下")
	}

	return dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		group, err := group_repo.Group().Find(txCtx, id)
		if err != nil {
			return err
		}

		allGroups, err := group_repo.Group().List(txCtx)
		if err != nil {
			return err
		}

		if err := validateGroupParent(id, targetParentID, allGroups); err != nil {
			return err
		}

		// 跨父级迁移
		if group.ParentID != targetParentID {
			if err := group_repo.Group().UpdateParentID(txCtx, id, targetParentID); err != nil {
				return err
			}
			group.ParentID = targetParentID
		}

		var siblings []*group_entity.Group
		for _, g := range allGroups {
			if g.ID == id {
				continue
			}
			if g.ParentID == targetParentID {
				siblings = append(siblings, g)
			}
		}
		siblings = append(siblings, group)

		reordered := sortutil.ReorderSiblings(siblings, id, beforeID,
			func(item *group_entity.Group) int64 { return item.ID },
		)

		for i, item := range reordered {
			newOrder := (i + 1) * 10
			if item.SortOrder == newOrder && item.ID != id {
				continue
			}
			if err := group_repo.Group().UpdateSortOrder(txCtx, item.ID, newOrder); err != nil {
				return err
			}
		}
		return nil
	})
}
