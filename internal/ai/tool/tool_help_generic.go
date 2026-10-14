package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// 通用资产的 help 内容：类型层（help <自定义类型标识>）与资产层（help <资产>）。它们经
// tool_handlers_unified.go 的两张登记表接进统一的 help 工具，分派器本身不认识通用资产。
func init() {
	assetHelpExtensions[asset_entity.AssetTypeGeneric] = renderGenericAssetHelp
	typeNameHelps = append(typeNameHelps, customTypeSlugHelp)
}

// customTypeSlugHelp 是通用资产的 typeNameHelp：typeName 是一个自定义类型标识时，文档是
// generic 的共用文档加上该类型的结构。
func customTypeSlugHelp(ctx context.Context, typeName string) (string, bool, error) {
	ct, err := custom_type_svc.CustomType().GetBySlug(ctx, typeName)
	if custom_type_svc.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	genericDoc, ok := permission.HelpFor(asset_entity.AssetTypeGeneric)
	if !ok {
		return "", false, fmt.Errorf("generic asset type has no help documentation yet")
	}
	return genericDoc + renderCustomTypeStructure(ct), true, nil
}

// renderCustomTypeStructure 渲染一个自定义类型的结构：执行方式、Base URL / 命令模板、
// 认证 / 环境变量绑定，字段列表及其 secret/required/default 属性（spec 「帮助、
// 技能与门禁」第 1 条），以及 HTTP 方式下接受的宿主连接字段。类型层（help <slug>）
// 与资产层（help <资产>）共用同一份渲染，避免两条路径各写一遍、内容跑偏。
func renderCustomTypeStructure(ct *custom_type_entity.CustomType) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nCustom type %q (%s), exec mode=%s.\n", ct.Slug, ct.Name, ct.ExecMode)
	switch ct.ExecMode {
	case custom_type_entity.ExecModeHTTP:
		if ct.HTTP != nil {
			fmt.Fprintf(&b, "Base URL: %s\n", ct.HTTP.BaseURL)
			// 与环境变量绑定一样给出值模板：模板是类型配置，不是注入值。
			for _, auth := range ct.HTTP.Auth {
				kind := auth.Type
				if auth.Name != "" {
					kind += " " + auth.Name
				}
				fmt.Fprintf(&b, "Auth: %s = %s\n", kind, strings.Join(auth.Values, " : "))
			}
		}
	case custom_type_entity.ExecModeCommand:
		if ct.Command != nil {
			if ct.Command.Template != "" {
				fmt.Fprintf(&b, "Command template: %s\n", ct.Command.Template)
			} else {
				b.WriteString("Command template: (none — exec runs the given command through the system shell)\n")
			}
			for _, e := range ct.Command.Env {
				fmt.Fprintf(&b, "Env: %s=%s\n", e.Name, e.Value)
			}
		}
	}
	b.WriteString("Fields:\n")
	for _, f := range ct.Fields {
		var attrs []string
		if f.Secret {
			attrs = append(attrs, "secret")
		}
		if f.Required {
			attrs = append(attrs, "required")
		}
		if f.Default != "" {
			attrs = append(attrs, fmt.Sprintf("default=%s", f.Default))
		}
		if len(attrs) > 0 {
			fmt.Fprintf(&b, "- %s (%s)\n", f.Name, strings.Join(attrs, ", "))
		} else {
			fmt.Fprintf(&b, "- %s\n", f.Name)
		}
	}
	// HTTP 方式的类型在字段之外还接受宿主连接字段（Design decision 20）；命令方式没有。
	if docs := assettype.GenericConnectionFieldDocs(ct); len(docs) > 0 {
		b.WriteString("\nConnection fields (config keys accepted next to the fields above; they set how the asset is reached):\n")
		for _, doc := range docs {
			fmt.Fprintf(&b, "- %s (%s): %s\n", doc.Name, doc.Type, doc.Notes)
		}
	}
	if strings.TrimSpace(ct.Usage) != "" {
		b.WriteString("\nUsage notes:\n")
		b.WriteString(ct.Usage)
		b.WriteString("\n")
	}
	return b.String()
}

// renderGenericAssetHelp 在类型结构之外追加这个通用资产实例的详情（spec 同一条第 2
// 项）：字段值——密钥字段只报告"set" / "not set"，从不带出明文，避免解密结果在这里被一个 Fprintf
// 之遥地打进模型上下文；缺值的必填字段标 missing；HTTP 方式下渲染后的实际地址与 SSH
// 隧道名；以及资产的 Description（备注，回答"这个实例里有什么"，与类型的使用说明区分
// 开——Design decision 11）。
func renderGenericAssetHelp(ctx context.Context, asset *asset_entity.Asset) (string, error) {
	resolved, err := custom_type_svc.CustomType().ResolveAsset(ctx, asset)
	if err != nil {
		return "", err
	}
	ct := resolved.Type

	missing := make(map[string]bool, len(resolved.Missing))
	for _, name := range resolved.Missing {
		missing[name] = true
	}

	var b strings.Builder
	b.WriteString(renderCustomTypeStructure(ct))

	b.WriteString("\nField values on this asset:\n")
	for _, f := range ct.Fields {
		switch {
		case missing[f.Name]:
			fmt.Fprintf(&b, "- %s: missing (required)\n", f.Name)
		case f.Secret && resolved.Values[f.Name] == "":
			fmt.Fprintf(&b, "- %s: not set\n", f.Name)
		case f.Secret:
			fmt.Fprintf(&b, "- %s: set\n", f.Name)
		default:
			fmt.Fprintf(&b, "- %s: %s\n", f.Name, resolved.Values[f.Name])
		}
	}

	if ct.ExecMode == custom_type_entity.ExecModeHTTP && ct.HTTP != nil {
		// Base URL 可以引用密钥字段：展示用渲染以掩码代替密钥（Hard invariant）。渲染失败
		// （如字段值拼不出绝对 http(s) URL）照实写在这一行，而不是悄悄省略——exec 会因同一个
		// 原因失败，help 正是模型该看到原因的地方；错误只含类型标识与原因，不含字段值。
		if addr, err := helper.RenderGenericDisplayBaseURL(ct, resolved.Values, time.Now()); err == nil {
			fmt.Fprintf(&b, "\nActual address: %s\n", addr)
		} else {
			fmt.Fprintf(&b, "\nActual address: unavailable (%v)\n", err)
		}
		line, err := renderGenericTunnelLine(ctx, asset)
		if err != nil {
			return "", err
		}
		b.WriteString(line)
	}

	if strings.TrimSpace(asset.Description) != "" {
		b.WriteString("\nDescription:\n")
		b.WriteString(asset.Description)
		b.WriteString("\n")
	}

	return b.String(), nil
}

// renderGenericTunnelLine 显示 HTTP 方式实际使用的网络路径，路径判定与传输层共用
// connpool.HTTPRouteFor（代理链 > SSH 隧道 > 直连；隧道在 Asset.SSHTunnelID，代理链在
// GenericConfig，Design decision 17）。代理链各跳的写法与新建 / 更新资产的审批摘要是同一份
// （assettype.ProxyChainSummary：每跳只有类型与目标，不含密码、Token，HTTP 隧道 URL 去掉
// userinfo / query）。查不到隧道 / 代理链 SSH 层资产（如已被删除）时仍照实列出那一跳的资产
// ID，而不是静默显示成"无隧道"——那会把一个配置不一致的问题伪装成"直连"。
func renderGenericTunnelLine(ctx context.Context, asset *asset_entity.Asset) (string, error) {
	cfg, err := asset.GetGenericConfig()
	if err != nil {
		return "", err
	}
	conn := connpool.GenericHTTPConn(asset, cfg)
	switch connpool.HTTPRouteFor(conn) {
	case connpool.HTTPRouteProxyChain:
		hops := assettype.ProxyChainSummary(asset_entity.NormalizeProxyChain(conn.ProxyChain), func(id int64) string {
			hop, err := asset_svc.Asset().Get(ctx, id)
			if err != nil {
				logger.Ctx(ctx).Warn("resolve proxy chain hop name for help", zap.Int64("assetID", id), zap.Error(err))
				return ""
			}
			return hop.Name
		})
		return fmt.Sprintf("Tunnel: proxy chain %s\n", strings.Join(hops, " -> ")), nil
	case connpool.HTTPRouteSSHTunnel:
		return fmt.Sprintf("Tunnel: %s\n", assetNameOrID(ctx, conn.TunnelID)), nil
	default:
		return "Tunnel: none (direct connection)\n", nil
	}
}

func assetNameOrID(ctx context.Context, id int64) string {
	a, err := asset_svc.Asset().Get(ctx, id)
	if err != nil {
		return fmt.Sprintf("asset id %d (name lookup failed: %v)", id, err)
	}
	return a.Name
}
