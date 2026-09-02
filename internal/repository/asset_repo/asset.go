package asset_repo

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
)

// AssetRepo 资产数据访问接口
type AssetRepo interface {
	Find(ctx context.Context, id int64) (*asset_entity.Asset, error)
	FindByName(ctx context.Context, name string) ([]*asset_entity.Asset, error)
	List(ctx context.Context, opts ListOptions) ([]*asset_entity.Asset, error)
	Create(ctx context.Context, asset *asset_entity.Asset) error
	Update(ctx context.Context, asset *asset_entity.Asset) error
	Delete(ctx context.Context, id int64) error
	MoveToGroup(ctx context.Context, fromGroupID, toGroupID int64) error
	DeleteByGroupID(ctx context.Context, groupID int64) error
	FindByCredentialID(ctx context.Context, credentialID int64) ([]*asset_entity.Asset, error)
	UpdateSortOrder(ctx context.Context, id int64, sortOrder int) error
	UpdateGroupID(ctx context.Context, id, groupID int64) error
	CountByTypes(ctx context.Context, types []string) (int64, error)
	// CountAgentAuthBySourceID 统计引用了指定 SSH Agent 来源的活动 SSH 资产数
	// （config 中 auth_type=agent 且 agent_source_id 匹配）。
	CountAgentAuthBySourceID(ctx context.Context, sourceID int64) (int64, error)
	// CountAgentAuthBySourceIDGroupByFingerprint 把引用了指定来源的活动 SSH 资产
	// 按所选身份指纹（agent_key_fingerprint）分组计数，用于逐把密钥展示使用数。
	CountAgentAuthBySourceIDGroupByFingerprint(ctx context.Context, sourceID int64) (map[string]int64, error)
	// ListAgentAuthBySourceID 列出引用了指定 SSH Agent 来源的活动 SSH 资产。
	ListAgentAuthBySourceID(ctx context.Context, sourceID int64) ([]*asset_entity.Asset, error)
}

// ListOptions 列表查询选项
type ListOptions struct {
	Type         string
	GroupID      int64
	ExactGroupID bool // 精确匹配 GroupID（包括 0），用于获取未分组资产
}

var defaultAsset AssetRepo

// Asset 获取AssetRepo实例
func Asset() AssetRepo {
	return defaultAsset
}

// RegisterAsset 注册AssetRepo实现
func RegisterAsset(i AssetRepo) {
	defaultAsset = i
}

// assetRepo 默认实现
type assetRepo struct{}

// NewAsset 创建默认实现
func NewAsset() AssetRepo {
	return &assetRepo{}
}

func (r *assetRepo) Find(ctx context.Context, id int64) (*asset_entity.Asset, error) {
	var asset asset_entity.Asset
	if err := db.Ctx(ctx).Where("id = ? AND status = ?", id, asset_entity.StatusActive).First(&asset).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *assetRepo) FindByName(ctx context.Context, name string) ([]*asset_entity.Asset, error) {
	var assets []*asset_entity.Asset
	if err := db.Ctx(ctx).Where("name = ? AND status = ?", name, asset_entity.StatusActive).Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

func (r *assetRepo) List(ctx context.Context, opts ListOptions) ([]*asset_entity.Asset, error) {
	var assets []*asset_entity.Asset
	query := db.Ctx(ctx).Where("status = ?", asset_entity.StatusActive)
	if opts.Type != "" {
		query = query.Where("type = ?", opts.Type)
	}
	if opts.ExactGroupID {
		query = query.Where("group_id = ?", opts.GroupID)
	} else if opts.GroupID > 0 {
		query = query.Where("group_id = ?", opts.GroupID)
	}
	if err := query.Order("sort_order ASC, id ASC").Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

func (r *assetRepo) Create(ctx context.Context, asset *asset_entity.Asset) error {
	return db.Ctx(ctx).Create(asset).Error
}

func (r *assetRepo) Update(ctx context.Context, asset *asset_entity.Asset) error {
	return db.Ctx(ctx).Save(asset).Error
}

func (r *assetRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&asset_entity.Asset{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":         asset_entity.StatusDeleted,
			"config":         "", // 清除敏感配置（含加密密码/密钥）
			"command_policy": "",
		}).Error
}

func (r *assetRepo) MoveToGroup(ctx context.Context, fromGroupID, toGroupID int64) error {
	return db.Ctx(ctx).Model(&asset_entity.Asset{}).
		Where("group_id = ? AND status = ?", fromGroupID, asset_entity.StatusActive).
		Update("group_id", toGroupID).Error
}

func (r *assetRepo) DeleteByGroupID(ctx context.Context, groupID int64) error {
	return db.Ctx(ctx).Model(&asset_entity.Asset{}).
		Where("group_id = ? AND status = ?", groupID, asset_entity.StatusActive).
		Updates(map[string]interface{}{
			"status":         asset_entity.StatusDeleted,
			"config":         "",
			"command_policy": "",
		}).Error
}

func (r *assetRepo) UpdateSortOrder(ctx context.Context, id int64, sortOrder int) error {
	return db.Ctx(ctx).Model(&asset_entity.Asset{}).Where("id = ?", id).Update("sort_order", sortOrder).Error
}

func (r *assetRepo) UpdateGroupID(ctx context.Context, id, groupID int64) error {
	return db.Ctx(ctx).Model(&asset_entity.Asset{}).Where("id = ?", id).Update("group_id", groupID).Error
}

func (r *assetRepo) FindByCredentialID(ctx context.Context, credentialID int64) ([]*asset_entity.Asset, error) {
	var assets []*asset_entity.Asset
	// Managed credentials can be owned by the primary asset config or by nested
	// Kafka companion configs. json_tree finds every credential_id leaf while
	// EXISTS keeps each referencing asset in the result only once.
	if err := db.Ctx(ctx).Where(`status = ? AND EXISTS (
		SELECT 1 FROM json_tree(assets.config) AS credential_refs
		WHERE credential_refs.key = 'credential_id'
			AND credential_refs.type IN ('integer', 'real')
			AND credential_refs.atom = ?
	)`, asset_entity.StatusActive, credentialID).Order("id ASC").Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

func (r *assetRepo) CountByTypes(ctx context.Context, types []string) (int64, error) {
	if len(types) == 0 {
		return 0, nil
	}
	var count int64
	err := db.Ctx(ctx).Model(&asset_entity.Asset{}).
		Where("type IN ? AND status = ?", types, asset_entity.StatusActive).
		Count(&count).Error
	return count, err
}

// agentSourceAssetQuery 是“引用指定 SSH Agent 来源的活动 SSH 资产”的查询条件。
// 来源既可用于 Agent 认证，也可用于 Agent 转发；两者都会让来源删除或端点变更影响
// 已保存资产，因此必须一起纳入引用查询。
func agentSourceAssetQuery(ctx context.Context, sourceID int64) *gorm.DB {
	return db.Ctx(ctx).
		Where("status = ? AND type = ?", asset_entity.StatusActive, asset_entity.AssetTypeSSH).
		Where(`(json_extract(config, '$.auth_type') = ? AND json_extract(config, '$.agent_source_id') = ?)
			OR (json_extract(config, '$.agent_forwarding') = 1 AND json_extract(config, '$.agent_forward_source_id') = ?)`, "agent", sourceID, sourceID)
}

// agentAuthSourceAssetQuery 是按身份指纹统计使用数专用条件；转发不选择单把密钥，
// 因此不能混入该分组查询。
func agentAuthSourceAssetQuery(ctx context.Context, sourceID int64) *gorm.DB {
	return db.Ctx(ctx).
		Where("status = ? AND type = ?", asset_entity.StatusActive, asset_entity.AssetTypeSSH).
		Where("json_extract(config, '$.auth_type') = ?", "agent").
		Where("json_extract(config, '$.agent_source_id') = ?", sourceID)
}

func (r *assetRepo) CountAgentAuthBySourceID(ctx context.Context, sourceID int64) (int64, error) {
	var count int64
	err := agentSourceAssetQuery(ctx, sourceID).Model(&asset_entity.Asset{}).Count(&count).Error
	return count, err
}

func (r *assetRepo) CountAgentAuthBySourceIDGroupByFingerprint(ctx context.Context, sourceID int64) (map[string]int64, error) {
	// Agent 模式资产在写入时必须携带规范指纹（validateSSHAgentContract），所以这里
	// 不存在指纹缺失的分组。
	const fingerprintExpr = "json_extract(config, '$.agent_key_fingerprint')"
	var rows []struct {
		Fingerprint string
		Total       int64
	}
	if err := agentAuthSourceAssetQuery(ctx, sourceID).Model(&asset_entity.Asset{}).
		Select(fingerprintExpr + " AS fingerprint, COUNT(*) AS total").
		Group(fingerprintExpr).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Fingerprint] = row.Total
	}
	return counts, nil
}

func (r *assetRepo) ListAgentAuthBySourceID(ctx context.Context, sourceID int64) ([]*asset_entity.Asset, error) {
	var assets []*asset_entity.Asset
	if err := agentSourceAssetQuery(ctx, sourceID).Order("id ASC").Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}
