package asset_svc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/pkg/sortutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// AssetSvc 资产业务接口
type AssetSvc interface {
	Get(ctx context.Context, id int64) (*asset_entity.Asset, error)
	FindByName(ctx context.Context, name string) ([]*asset_entity.Asset, error)
	List(ctx context.Context, assetType string, groupID int64) ([]*asset_entity.Asset, error)
	AgentReferenceCount(ctx context.Context, sourceID int64) (int64, error)
	AgentReferenceCountByFingerprint(ctx context.Context, sourceID int64) (map[string]int64, error)
	AgentReferencingAssets(ctx context.Context, sourceID int64) ([]*asset_entity.Asset, error)
	Create(ctx context.Context, asset *asset_entity.Asset) error
	Update(ctx context.Context, asset *asset_entity.Asset) error
	UpdateWithinTransaction(ctx context.Context, asset *asset_entity.Asset) error
	Invalidate(ctx context.Context, id int64)
	Delete(ctx context.Context, id int64) error
	DeleteByGroup(ctx context.Context, groupID int64) ([]*asset_entity.Asset, error)
	MoveFromGroup(ctx context.Context, groupID int64) error
	Move(ctx context.Context, id int64, direction string) error
	Reorder(ctx context.Context, id, targetGroupID, beforeID int64) error
}

type assetSvc struct{}

var defaultAsset = &assetSvc{}

// Asset 获取AssetSvc实例
func Asset() AssetSvc {
	return defaultAsset
}

func (s *assetSvc) Get(ctx context.Context, id int64) (*asset_entity.Asset, error) {
	return asset_repo.Asset().Find(ctx, id)
}

func (s *assetSvc) FindByName(ctx context.Context, name string) ([]*asset_entity.Asset, error) {
	return asset_repo.Asset().FindByName(ctx, name)
}

func (s *assetSvc) List(ctx context.Context, assetType string, groupID int64) ([]*asset_entity.Asset, error) {
	return asset_repo.Asset().List(ctx, asset_repo.ListOptions{
		Type:    assetType,
		GroupID: groupID,
	})
}

func (s *assetSvc) AgentReferenceCount(ctx context.Context, sourceID int64) (int64, error) {
	return asset_repo.Asset().CountAgentAuthBySourceID(ctx, sourceID)
}

func (s *assetSvc) AgentReferenceCountByFingerprint(ctx context.Context, sourceID int64) (map[string]int64, error) {
	return asset_repo.Asset().CountAgentAuthBySourceIDGroupByFingerprint(ctx, sourceID)
}

func (s *assetSvc) AgentReferencingAssets(ctx context.Context, sourceID int64) ([]*asset_entity.Asset, error) {
	return asset_repo.Asset().ListAgentAuthBySourceID(ctx, sourceID)
}

func (s *assetSvc) Create(ctx context.Context, asset *asset_entity.Asset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	now := time.Now().Unix()
	asset.Createtime = now
	asset.Updatetime = now
	asset.Status = asset_entity.StatusActive
	// 未设置命令策略时，根据资产类型应用默认拒绝列表
	if asset.CmdPolicy == "" {
		if p, ok := policy.GetDefaultPolicyOf(asset.Type); ok {
			data, err := json.Marshal(p)
			if err != nil {
				logger.Default().Error("marshal default policy", zap.Error(err))
			} else {
				asset.CmdPolicy = string(data)
			}
		}
	}
	return asset_repo.Asset().Create(ctx, asset)
}

func (s *assetSvc) Update(ctx context.Context, asset *asset_entity.Asset) error {
	if err := s.UpdateWithinTransaction(ctx, asset); err != nil {
		return err
	}
	s.Invalidate(ctx, asset.ID)
	return nil
}

// UpdateWithinTransaction persists an update without invalidating connections before
// an outer transaction commits. Callers must invoke Invalidate after successful commit.
func (s *assetSvc) UpdateWithinTransaction(ctx context.Context, asset *asset_entity.Asset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	asset.Updatetime = time.Now().Unix()
	return asset_repo.Asset().Update(ctx, asset)
}

// Invalidate drops cached connections after a successful asset update commit.
func (s *assetSvc) Invalidate(ctx context.Context, id int64) {
	// 连接配置可能变了（主机、端口、口令、隧道），缓存/池化的连接是按旧配置拨出去的，
	// 不丢掉的话下一次操作照旧复用它，用户会觉得"改了没生效"。只失效不关会话——
	// 用户开着的终端不该因为改了个字段被掐断。
	assetconn.InvalidateAsset(ctx, id)
}

func (s *assetSvc) Delete(ctx context.Context, id int64) error {
	if err := asset_repo.Asset().Delete(ctx, id); err != nil {
		return err
	}
	// 资产已经删了，各协议管理器里挂着的会话/客户端必须跟着断开，否则会一直连着一个不存在的资产。
	// 关闭失败由 assetconn 内部按 closer 记日志：删除本身已经成功，没有可回滚的东西。
	assetconn.CloseAsset(ctx, id)
	return nil
}

// DeleteByGroup 删除指定分组下的全部资产，并返回删除前的资产信息。
// 调用方可用返回值在事务提交后断开连接、写入带名称和类型的审计记录。
func (s *assetSvc) DeleteByGroup(ctx context.Context, groupID int64) ([]*asset_entity.Asset, error) {
	assets, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{GroupID: groupID, ExactGroupID: true})
	if err != nil {
		return nil, err
	}
	if err := asset_repo.Asset().DeleteByGroupID(ctx, groupID); err != nil {
		return nil, err
	}
	return assets, nil
}

// MoveFromGroup 将指定分组下的资产移到未分组。
func (s *assetSvc) MoveFromGroup(ctx context.Context, groupID int64) error {
	return asset_repo.Asset().MoveToGroup(ctx, groupID, 0)
}

// Move 移动资产排序（up/down/top）
func (s *assetSvc) Move(ctx context.Context, id int64, direction string) error {
	asset, err := asset_repo.Asset().Find(ctx, id)
	if err != nil {
		return err
	}
	siblings, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{GroupID: asset.GroupID, ExactGroupID: true})
	if err != nil {
		return err
	}
	return sortutil.MoveItem(id, direction, siblings,
		func(item *asset_entity.Asset) int64 { return item.ID },
		func(item *asset_entity.Asset) int { return item.SortOrder },
		func(itemID int64, order int) error {
			return asset_repo.Asset().UpdateSortOrder(ctx, itemID, order)
		},
	)
}

// Reorder 把 id 移动到 targetGroupID 内 beforeID 之前。beforeID == 0 表示插到末尾。
// 跨分组时同步 GroupID。重排目标容器内所有兄弟的 sort_order，等间距 (10/20/30...)。
func (s *assetSvc) Reorder(ctx context.Context, id, targetGroupID, beforeID int64) error {
	return dbutil.WithTransaction(ctx, func(txCtx context.Context) error {
		asset, err := asset_repo.Asset().Find(txCtx, id)
		if err != nil {
			return err
		}

		// 跨分组迁移
		if asset.GroupID != targetGroupID {
			if err := asset_repo.Asset().UpdateGroupID(txCtx, id, targetGroupID); err != nil {
				return err
			}
			asset.GroupID = targetGroupID
		}

		siblings, err := asset_repo.Asset().List(txCtx, asset_repo.ListOptions{GroupID: targetGroupID, ExactGroupID: true})
		if err != nil {
			return err
		}

		reordered := sortutil.ReorderSiblings(siblings, id, beforeID,
			func(item *asset_entity.Asset) int64 { return item.ID },
		)

		for i, item := range reordered {
			newOrder := (i + 1) * 10
			if item.SortOrder == newOrder && item.ID != id {
				continue
			}
			if err := asset_repo.Asset().UpdateSortOrder(txCtx, item.ID, newOrder); err != nil {
				return err
			}
		}
		return nil
	})
}
