package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// GenericConnTestInput 是通用资产「测试连接」（conntest 注册表，资产表单里尚未保存的内容）
// 的 configJSON 契约（docs/specs/2026-09-28-generic-asset.md「通用资产」测试连接一条）。
type GenericConnTestInput struct {
	// AssetID 非 0 表示编辑已有资产：Values 里没有给出的字段——表单上未改动的密钥——沿用
	// 该资产已存的值；给出的字段按表单内容覆盖。
	AssetID    int64  `json:"asset_id,omitempty"`
	CustomType string `json:"custom_type"`
	// Values 与 put_asset 的 config 同形：字段名 → 字符串；密钥字段也可以是 {"credential_id": N}。
	Values map[string]any `json:"values,omitempty"`
	// SSHTunnelID 是表单上选的 SSH 隧道（保存后落在 Asset.SSHTunnelID）。
	SSHTunnelID int64 `json:"ssh_tunnel_id,omitempty"`
	asset_entity.GenericConnection
}

// ProbeGenericConnection 是通用资产的 conntest.DetailedTestFunc：渲染 Base URL 与认证后对
// Base URL 发一次 GET，拨号、重定向与注入规则与 exec 完全相同（sendGenericHTTP）。收到任何
// HTTP 响应都算连通，详情给出状态行与经过的路径；401 / 403 报认证失败；连接、TLS、隧道
// 错误原样返回。它不是对资产的操作，不经过策略。plainPassword 不适用（密钥在 Values 里）。
func ProbeGenericConnection(ctx context.Context, configJSON, _ string) (string, error) {
	var in GenericConnTestInput
	if err := json.Unmarshal([]byte(configJSON), &in); err != nil {
		return "", fmt.Errorf("解析测试连接配置失败: %w", err)
	}
	ct, err := custom_type_svc.CustomType().GetBySlug(ctx, in.CustomType)
	if err != nil {
		return "", fmt.Errorf("自定义类型 %q: %w", in.CustomType, err)
	}
	if ct.ExecMode != custom_type_entity.ExecModeHTTP {
		return "", fmt.Errorf("测试连接只适用于 HTTP 执行方式的自定义类型；%q 的执行方式是 %s", ct.Slug, ct.ExecMode)
	}
	asset, err := genericConnTestAsset(ctx, ct, &in)
	if err != nil {
		return "", err
	}
	t, _, err := resolveGenericTarget(ctx, asset)
	if err != nil {
		return "", err
	}
	resp, err := sendGenericHTTP(ctx, t, &HTTPCommand{Method: http.MethodGet}, nil)
	if err != nil {
		return "", err
	}
	defer resp.close()

	status := statusLine(resp.Response)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("认证失败: %s", status)
	}
	return status + "（" + describeHTTPRoute(ctx, resp.route, asset.SSHTunnelID) + "）", nil
}

// genericConnTestAsset 按表单内容构造一台未保存的通用资产。字段值经 put_asset 同一条
// assettype 契约校验与存储（密钥加密、托管凭据校验），编辑时以已存资产为底只改给出的字段。
func genericConnTestAsset(ctx context.Context, ct *custom_type_entity.CustomType, in *GenericConnTestInput) (*asset_entity.Asset, error) {
	asset := &asset_entity.Asset{Name: ct.Slug, Type: asset_entity.AssetTypeGeneric}
	if in.AssetID > 0 {
		stored, err := asset_svc.Asset().Get(ctx, in.AssetID)
		if err != nil {
			return nil, err
		}
		cfg, err := stored.GetGenericConfig()
		if err != nil {
			return nil, err
		}
		if cfg.CustomType != ct.Slug {
			return nil, fmt.Errorf("资产 %q 的自定义类型是 %q，不是 %q", stored.Name, cfg.CustomType, ct.Slug)
		}
		copied := *stored
		asset = &copied
		prepared, err := assettype.PrepareUpdate(ctx, asset, in.Values)
		if err != nil {
			return nil, err
		}
		if err := prepared.Handler.ApplyUpdateArgs(ctx, asset, prepared.Config); err != nil {
			return nil, err
		}
	} else {
		if err := asset.SetGenericConfig(&asset_entity.GenericConfig{CustomType: ct.Slug}); err != nil {
			return nil, err
		}
		prepared, err := assettype.PrepareCreate(ctx, asset, in.Values)
		if err != nil {
			return nil, err
		}
		if err := prepared.Handler.ApplyCreateArgs(ctx, asset, prepared.Config); err != nil {
			return nil, err
		}
	}
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return nil, err
	}
	cfg.GenericConnection = in.GenericConnection
	if err := asset.SetGenericConfig(cfg); err != nil {
		return nil, err
	}
	asset.SSHTunnelID = in.SSHTunnelID
	return asset, nil
}

func describeHTTPRoute(ctx context.Context, route connpool.HTTPRoute, tunnelID int64) string {
	switch route {
	case connpool.HTTPRouteSSHTunnel:
		tunnel, err := asset_svc.Asset().Get(ctx, tunnelID)
		if err != nil {
			return fmt.Sprintf("经 SSH 隧道：资产 %d（名称查询失败: %v）", tunnelID, err)
		}
		return "经 SSH 隧道：" + tunnel.Name
	case connpool.HTTPRouteProxyChain:
		return "经代理链"
	default:
		return "直连"
	}
}
