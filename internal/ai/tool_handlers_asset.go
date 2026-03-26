package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// --- 工具 handler 实现 ---

// safeAssetView 返回不含敏感信息的资产视图
type safeAssetView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	GroupID     int64  `json:"group_id"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
	Createtime  int64  `json:"createtime"`
	Updatetime  int64  `json:"updatetime"`
	// 连接信息（不含密码/密钥）
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	AuthType string `json:"auth_type,omitempty"`
	// Database 专属
	Driver   string `json:"driver,omitempty"`
	Database string `json:"database,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	// Redis 专属
	RedisDB int `json:"redis_db,omitempty"`
}

// safeGroupListView 列表视图（不含描述）
type safeGroupListView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ParentID  int64  `json:"parent_id"`
	Icon      string `json:"icon,omitempty"`
	SortOrder int    `json:"sort_order"`
}

// safeGroupDetailView 详情视图（含描述）
type safeGroupDetailView struct {
	safeGroupListView
	Description string `json:"description,omitempty"`
}

func toSafeView(a *asset_entity.Asset) safeAssetView {
	v := safeAssetView{
		ID:          a.ID,
		Name:        a.Name,
		Type:        a.Type,
		GroupID:     a.GroupID,
		Description: a.Description,
		SortOrder:   a.SortOrder,
		Createtime:  a.Createtime,
		Updatetime:  a.Updatetime,
	}
	switch a.Type {
	case asset_entity.AssetTypeSSH:
		if cfg, err := a.GetSSHConfig(); err == nil && cfg != nil {
			v.Host = cfg.Host
			v.Port = cfg.Port
			v.Username = cfg.Username
			v.AuthType = cfg.AuthType
		}
	case asset_entity.AssetTypeDatabase:
		if cfg, err := a.GetDatabaseConfig(); err == nil && cfg != nil {
			v.Host = cfg.Host
			v.Port = cfg.Port
			v.Username = cfg.Username
			v.Driver = string(cfg.Driver)
			v.Database = cfg.Database
			v.ReadOnly = cfg.ReadOnly
		}
	case asset_entity.AssetTypeRedis:
		if cfg, err := a.GetRedisConfig(); err == nil && cfg != nil {
			v.Host = cfg.Host
			v.Port = cfg.Port
			v.Username = cfg.Username
			v.RedisDB = cfg.Database
		}
	}
	return v
}

func handleListAssets(ctx context.Context, args map[string]any) (string, error) {
	assetType := argString(args, "asset_type")
	groupID := argInt64(args, "group_id")
	assets, err := asset_svc.Asset().List(ctx, assetType, groupID)
	if err != nil {
		return "", err
	}
	views := make([]safeAssetView, len(assets))
	for i, a := range assets {
		views[i] = toSafeView(a)
		views[i].Description = "" // list 不返回描述，通过 get_asset 查看
	}
	data, err := json.Marshal(views)
	if err != nil {
		logger.Default().Error("marshal asset list", zap.Error(err))
		return "", fmt.Errorf("序列化资产列表失败: %w", err)
	}
	return string(data), nil
}

func handleGetAsset(ctx context.Context, args map[string]any) (string, error) {
	id := argInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("缺少参数 id")
	}
	asset, err := asset_svc.Asset().Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	data, err := json.Marshal(toSafeView(asset))
	if err != nil {
		logger.Default().Error("marshal asset detail", zap.Error(err))
		return "", fmt.Errorf("序列化资产详情失败: %w", err)
	}
	return string(data), nil
}

func handleAddAsset(ctx context.Context, args map[string]any) (string, error) {
	name := argString(args, "name")
	host := argString(args, "host")
	port := argInt(args, "port")
	username := argString(args, "username")
	if name == "" || host == "" || port == 0 || username == "" {
		return "", fmt.Errorf("缺少必要参数 (name, host, port, username)")
	}

	assetType := argString(args, "type")
	if assetType == "" {
		assetType = asset_entity.AssetTypeSSH
	}
	groupID := argInt64(args, "group_id")
	description := argString(args, "description")

	icon := argString(args, "icon")

	asset := &asset_entity.Asset{
		Name:        name,
		Type:        assetType,
		Icon:        icon,
		GroupID:     groupID,
		Description: description,
	}

	switch assetType {
	case asset_entity.AssetTypeSSH:
		authType := argString(args, "auth_type")
		if authType == "" {
			authType = "password"
		}
		if err := asset.SetSSHConfig(&asset_entity.SSHConfig{
			Host:     host,
			Port:     port,
			Username: username,
			AuthType: authType,
		}); err != nil {
			logger.Default().Warn("set SSH config for new asset", zap.Error(err))
		}
	case asset_entity.AssetTypeDatabase:
		driver := asset_entity.DatabaseDriver(argString(args, "driver"))
		if driver == "" {
			return "", fmt.Errorf("数据库类型必须指定 driver (mysql 或 postgresql)")
		}
		dbCfg := &asset_entity.DatabaseConfig{
			Driver:     driver,
			Host:       host,
			Port:       port,
			Username:   username,
			Database:   argString(args, "database"),
			ReadOnly:   argString(args, "read_only") == "true",
			SSHAssetID: argInt64(args, "ssh_asset_id"),
		}
		if err := asset.SetDatabaseConfig(dbCfg); err != nil {
			logger.Default().Warn("set database config for new asset", zap.Error(err))
		}
	case asset_entity.AssetTypeRedis:
		redisCfg := &asset_entity.RedisConfig{
			Host:       host,
			Port:       port,
			Username:   username,
			SSHAssetID: argInt64(args, "ssh_asset_id"),
		}
		if err := asset.SetRedisConfig(redisCfg); err != nil {
			logger.Default().Warn("set Redis config for new asset", zap.Error(err))
		}
	default:
		return "", fmt.Errorf("不支持的资产类型: %s", assetType)
	}

	if err := asset_svc.Asset().Create(ctx, asset); err != nil {
		return "", fmt.Errorf("创建资产失败: %w", err)
	}
	return fmt.Sprintf(`{"id":%d,"message":"资产创建成功"}`, asset.ID), nil
}

func handleUpdateAsset(ctx context.Context, args map[string]any) (string, error) {
	id := argInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("缺少参数 id")
	}

	asset, err := asset_svc.Asset().Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}

	if name := argString(args, "name"); name != "" {
		asset.Name = name
	}
	if desc := argString(args, "description"); desc != "" {
		asset.Description = desc
	}
	if _, ok := args["group_id"]; ok {
		asset.GroupID = argInt64(args, "group_id")
	}
	if icon := argString(args, "icon"); icon != "" {
		asset.Icon = icon
	}

	switch asset.Type {
	case asset_entity.AssetTypeSSH:
		sshCfg, err := asset.GetSSHConfig()
		if err != nil {
			logger.Default().Warn("get SSH config for asset update", zap.Error(err))
		}
		if sshCfg != nil {
			if host := argString(args, "host"); host != "" {
				sshCfg.Host = host
			}
			if port := argInt(args, "port"); port > 0 {
				sshCfg.Port = port
			}
			if username := argString(args, "username"); username != "" {
				sshCfg.Username = username
			}
			if err := asset.SetSSHConfig(sshCfg); err != nil {
				logger.Default().Warn("set SSH config for updated asset", zap.Error(err))
			}
		}
	case asset_entity.AssetTypeDatabase:
		dbCfg, err := asset.GetDatabaseConfig()
		if err != nil {
			logger.Default().Warn("get database config for asset update", zap.Error(err))
		}
		if dbCfg != nil {
			if host := argString(args, "host"); host != "" {
				dbCfg.Host = host
			}
			if port := argInt(args, "port"); port > 0 {
				dbCfg.Port = port
			}
			if username := argString(args, "username"); username != "" {
				dbCfg.Username = username
			}
			if db := argString(args, "database"); db != "" {
				dbCfg.Database = db
			}
			if err := asset.SetDatabaseConfig(dbCfg); err != nil {
				logger.Default().Warn("set database config for updated asset", zap.Error(err))
			}
		}
	case asset_entity.AssetTypeRedis:
		redisCfg, err := asset.GetRedisConfig()
		if err != nil {
			logger.Default().Warn("get redis config for asset update", zap.Error(err))
		}
		if redisCfg != nil {
			if host := argString(args, "host"); host != "" {
				redisCfg.Host = host
			}
			if port := argInt(args, "port"); port > 0 {
				redisCfg.Port = port
			}
			if username := argString(args, "username"); username != "" {
				redisCfg.Username = username
			}
			if err := asset.SetRedisConfig(redisCfg); err != nil {
				logger.Default().Warn("set Redis config for updated asset", zap.Error(err))
			}
		}
	}

	if err := asset_svc.Asset().Update(ctx, asset); err != nil {
		return "", fmt.Errorf("更新资产失败: %w", err)
	}
	return `{"message":"资产更新成功"}`, nil
}

func handleListGroups(ctx context.Context, _ map[string]any) (string, error) {
	groups, err := group_repo.Group().List(ctx)
	if err != nil {
		return "", fmt.Errorf("获取分组失败: %w", err)
	}
	views := make([]safeGroupListView, len(groups))
	for i, g := range groups {
		views[i] = safeGroupListView{
			ID:        g.ID,
			Name:      g.Name,
			ParentID:  g.ParentID,
			Icon:      g.Icon,
			SortOrder: g.SortOrder,
		}
	}
	data, err := json.Marshal(views)
	if err != nil {
		logger.Default().Error("marshal group list", zap.Error(err))
		return "", fmt.Errorf("序列化分组列表失败: %w", err)
	}
	return string(data), nil
}

func handleGetGroup(ctx context.Context, args map[string]any) (string, error) {
	id := argInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("缺少参数 id")
	}
	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return "", fmt.Errorf("分组不存在: %w", err)
	}
	view := safeGroupDetailView{
		safeGroupListView: safeGroupListView{
			ID:        group.ID,
			Name:      group.Name,
			ParentID:  group.ParentID,
			Icon:      group.Icon,
			SortOrder: group.SortOrder,
		},
		Description: group.Description,
	}
	data, err := json.Marshal(view)
	if err != nil {
		logger.Default().Error("marshal group detail", zap.Error(err))
		return "", fmt.Errorf("序列化分组详情失败: %w", err)
	}
	return string(data), nil
}
