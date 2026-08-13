package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_query_svc"
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
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	Username  string `json:"username,omitempty"`
	AuthType  string `json:"auth_type,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Clipboard bool   `json:"clipboard,omitempty"`
	// SSH Agent 认证资产对称返回来源 ID 与规范指纹（与桌面保存校验一致）。
	// 端点/公钥/备注/签名/挑战答案绝不进入安全视图。
	AgentSourceID       int64  `json:"agent_source_id,omitempty"`
	AgentKeyFingerprint string `json:"agent_key_fingerprint,omitempty"`
	// Database 专属
	Driver   string `json:"driver,omitempty"`
	Database string `json:"database,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	// Redis 专属
	RedisDB int `json:"redis_db,omitempty"`
	// K8s 专属
	Namespace   string `json:"namespace,omitempty"`
	K8sContext  string `json:"context,omitempty"`
	SSHTunnelID int64  `json:"ssh_tunnel_id,omitempty"`
	// Serial 专属（COM/TTY 类设备，没有 host/port 概念）
	PortPath       string                                    `json:"port_path,omitempty"`
	BaudRate       int                                       `json:"baud_rate,omitempty"`
	DataBits       int                                       `json:"data_bits,omitempty"`
	StopBits       string                                    `json:"stop_bits,omitempty"`
	Parity         string                                    `json:"parity,omitempty"`
	FlowControl    string                                    `json:"flow_control,omitempty"`
	Authentication *credential_query_svc.AssetAuthentication `json:"authentication,omitempty"`
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
	if h, ok := assettype.Get(a.Type); ok {
		if fields := h.SafeView(a); fields != nil {
			if val, ok := fields["host"].(string); ok {
				v.Host = val
			}
			if val, ok := fields["port"].(int); ok {
				v.Port = val
			}
			if val, ok := fields["username"].(string); ok {
				v.Username = val
			}
			if val, ok := fields["driver"].(string); ok {
				v.Driver = val
			}
			if val, ok := fields["database"].(string); ok {
				v.Database = val
			}
			if val, ok := fields["read_only"].(bool); ok {
				v.ReadOnly = val
			}
			if val, ok := fields["redis_db"].(int); ok {
				v.RedisDB = val
			}
			if val, ok := fields["auth_type"].(string); ok {
				v.AuthType = val
			}
			if val, ok := fields["agent_source_id"].(int64); ok {
				v.AgentSourceID = val
			}
			if val, ok := fields["agent_key_fingerprint"].(string); ok {
				v.AgentKeyFingerprint = val
			}
			if val, ok := fields["domain"].(string); ok {
				v.Domain = val
			}
			if val, ok := fields["width"].(int); ok {
				v.Width = val
			}
			if val, ok := fields["height"].(int); ok {
				v.Height = val
			}
			if val, ok := fields["clipboard"].(bool); ok {
				v.Clipboard = val
			}
			if val, ok := fields["namespace"].(string); ok {
				v.Namespace = val
			}
			if val, ok := fields["context"].(string); ok {
				v.K8sContext = val
			}
			if val, ok := fields["ssh_tunnel_id"].(int64); ok {
				v.SSHTunnelID = val
			}
			if val, ok := fields["port_path"].(string); ok {
				v.PortPath = val
			}
			if val, ok := fields["baud_rate"].(int); ok {
				v.BaudRate = val
			}
			if val, ok := fields["data_bits"].(int); ok {
				v.DataBits = val
			}
			if val, ok := fields["stop_bits"].(string); ok {
				v.StopBits = val
			}
			if val, ok := fields["parity"].(string); ok {
				v.Parity = val
			}
			if val, ok := fields["flow_control"].(string); ok {
				v.FlowControl = val
			}
		}
	}
	return v
}

func handleListAssets(ctx context.Context, args map[string]any) (string, error) {
	assetType := aictx.ArgString(args, "asset_type")
	groupID := aictx.ArgInt64(args, "group_id")
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
		return "", fmt.Errorf("failed to marshal asset list: %w", err)
	}
	return string(data), nil
}

func handleGetAsset(ctx context.Context, args map[string]any) (string, error) {
	id := aictx.ArgInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("missing required parameter: id")
	}
	asset, err := asset_svc.Asset().Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("asset not found: %w", err)
	}
	view := toSafeView(asset)
	if h, ok := assettype.Get(asset.Type); ok {
		association, applicable, err := assettype.AuthenticationAssociationOf(h, asset)
		if err != nil {
			return "", err
		}
		if applicable {
			view.Authentication, err = credential_query_svc.DefaultAssetAuthentication().GetAssetAuthentication(ctx, credential_query_svc.AssetAuthenticationRequest{
				Type: association.Type, Ref: association.Ref, Fingerprint: association.Fingerprint,
			})
			if err != nil {
				return "", err
			}
		}
	}
	data, err := json.Marshal(view)
	if err != nil {
		logger.Default().Error("marshal asset detail", zap.Error(err))
		return "", fmt.Errorf("failed to marshal asset detail: %w", err)
	}
	return string(data), nil
}

func handleListGroups(ctx context.Context, _ map[string]any) (string, error) {
	groups, err := group_repo.Group().List(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list groups: %w", err)
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
		return "", fmt.Errorf("failed to marshal group list: %w", err)
	}
	return string(data), nil
}

func handleGetGroup(ctx context.Context, args map[string]any) (string, error) {
	id := aictx.ArgInt64(args, "id")
	if id == 0 {
		return "", fmt.Errorf("missing required parameter: id")
	}
	group, err := group_repo.Group().Find(ctx, id)
	if err != nil {
		return "", fmt.Errorf("group not found: %w", err)
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
		return "", fmt.Errorf("failed to marshal group detail: %w", err)
	}
	return string(data), nil
}
