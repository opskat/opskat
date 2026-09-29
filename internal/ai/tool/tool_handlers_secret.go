package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// docs/specs/2026-09-28-generic-asset.md「取值」: get_asset_secret is the single handler
// shared by the get_asset_secret AI tool (tools_unified.go) and `opsctl secret get`
// (cmd/opsctl/command/secret.go, via tool.AllToolDefs()). It is the only way to read a
// generic asset's field value back out, secret or not — built-in typed assets are out of
// scope entirely (that would bypass their own credential handling).
func init() {
	// 取值的审计投影：审计只记录资产、字段名和判定结果，不记录值（spec「取值」"审计"）。
	// 注册而不是在 audit.WriteToolCall 里按工具名分支——AGENTS.md「扩展由注册」同一原则，
	// 也是这份机制存在的唯一理由：get_asset_secret 是第一个"返回值本身即敏感数据"的工具。
	audit.RegisterResultProjector("get_asset_secret", projectGetAssetSecretResult)
}

// handleGetAssetSecret 执行顺序（与 handleExec 同一套"无副作用检查先做完"的道理，只是
// 这里唯一有副作用的步骤是审批弹窗）：
//  1. 解析资产 + 校验是通用资产——内置类型的密码走各自的凭据机制，不经这条路。
//  2. ResolveAsset 拿到类型结构与已解密的字段值。
//  3. 按字段名查找：不存在报错并列出可用字段名；是必填但无值（ResolveAsset.Missing）报错。
//  4. 非密钥字段直接返回值，不做权限检查（spec："本来就在详情和 help 里可见"）。
//  5. 密钥字段按 `secret:<字段>` 走权限检查；NeedConfirm 会经 HandleConfirm 弹审批
//     （detail 写明明文会输出给调用方、来自 AI 时会进入对话并发给模型服务商）；Deny 时
//     返回值是判定说明文字，绝不是明文本身。
func handleGetAssetSecret(ctx context.Context, args map[string]any) (string, error) {
	ref := aictx.ArgString(args, "asset")
	fieldName := aictx.ArgString(args, "field")

	asset, field, resolved, err := LookupGenericSecretField(ctx, ref, fieldName)
	if err != nil {
		return "", err
	}

	if !field.Secret {
		return resolved.Values[fieldName], nil
	}

	// checker 为 nil 只在 opsctl 已经跑过 requireApproval 的路径上合法
	// （permission.WithPreapproved）；其余情况 fail-closed，见 handleExec 同一处调用的
	// 注释——漏接线不能等于放行。
	checker, err := permission.RequireCheckerOrPreapproved(ctx)
	if err != nil {
		return "", err
	}
	if checker != nil {
		result := checker.CheckForAsset(ctx, asset.ID, asset_entity.AssetTypeGeneric,
			secretSubjectFor(fieldName), SecretApprovalDetail(ctx))
		aictx.RecordDecision(ctx, result)
		if result.Decision != aictx.Allow {
			return result.Message, nil
		}
	}
	return resolved.Values[fieldName], nil
}

// secretSubjectFor 是取值的匹配对象（spec「策略、审批与审计」：`secret:<字段>`），复用
// permission.SecretSubjectPrefix——两边不必各自维护一份字面量。
func secretSubjectFor(fieldName string) string {
	return permission.SecretSubjectPrefix + fieldName
}

// LookupGenericSecretField 解析资产 + 校验通用资产 + 按字段名在其自定义类型里查找，是
// handleGetAssetSecret 与 opsctl `secret get`（cmd/opsctl/command/secret.go）共用的唯一
// 解析实现：opsctl 需要在弹出审批对话框之前就知道这个字段是不是密钥字段，而这个判断只有
// 解析出该资产的自定义类型结构才能做。
func LookupGenericSecretField(ctx context.Context, ref, fieldName string) (*asset_entity.Asset, *custom_type_entity.Field, *custom_type_svc.Resolved, error) {
	asset, err := assetref.Resolve(ctx, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if !asset.IsGeneric() {
		return nil, nil, nil, fmt.Errorf(
			"asset %q (type=%s) is not a generic asset; secret get is only supported for generic assets", asset.Name, asset.Type)
	}

	resolved, err := custom_type_svc.CustomType().ResolveAsset(ctx, asset)
	if err != nil {
		return nil, nil, nil, err
	}

	names := make([]string, 0, len(resolved.Type.Fields))
	var field *custom_type_entity.Field
	for i := range resolved.Type.Fields {
		f := &resolved.Type.Fields[i]
		names = append(names, f.Name)
		if f.Name == fieldName {
			field = f
		}
	}
	if field == nil {
		return nil, nil, nil, fmt.Errorf(
			"asset %q has no field %q; available fields: %s", asset.Name, fieldName, strings.Join(names, ", "))
	}
	for _, missing := range resolved.Missing {
		if missing == fieldName {
			return nil, nil, nil, fmt.Errorf("asset %q field %q has no value set", asset.Name, fieldName)
		}
	}
	return asset, field, resolved, nil
}

// SecretApprovalDetail 是取值审批弹窗的说明文字（spec「策略、审批与审计」："弹窗...写明
// 明文将输出给调用方；来自 AI 时会进入对话上下文并发送给模型服务商"）。导出给
// cmd/opsctl/command/secret.go：opsctl 自己的 requireApproval 走桌面弹窗/终端审批，
// 不经过 handleGetAssetSecret 里下面这条 CheckForAsset 调用，所以两边各自把这句话
// 放进各自请求的 Detail 字段——单一实现，不是两份文案。
func SecretApprovalDetail(ctx context.Context) string {
	return policy.PolicyMsg(ctx,
		"The plaintext value will be output to the caller. If this call came from the AI, "+
			"it enters the conversation and is sent to the model provider.",
		"明文将输出给调用方；来自 AI 时会进入对话上下文并发送给模型服务商。")
}

// projectGetAssetSecretResult 是 get_asset_secret 的审计 Result 投影（spec「取值」"审计：
// 只记录资产、字段名和判定结果，不记录值"）：故意不看 info.Result 本身的内容——那正是
// 允许放行时装着明文的字段，唯一安全的做法是完全不引用它，只从 ArgsJSON 里的字段名和
// info.Decision/info.Error 里的判定结果重新拼一句话。
func projectGetAssetSecretResult(info audit.ToolCallInfo) string {
	field := secretAuditFieldName(info.ArgsJSON)
	switch {
	// Decision 优先于 Error 判断：AI 侧的 auditMiddleware 把一次记录过的 Deny 决策强制
	// 转成 c.Output.IsError=true（runner/hooks.go），所以到这里时 info.Error 往往也非
	// nil——但那是"决策=拒绝"的既有结论，不是执行失败，用 decision=deny 更准确。
	case info.Decision != nil && info.Decision.Decision == aictx.Deny:
		return fmt.Sprintf("field=%s decision=deny", field)
	case info.Error != nil:
		return fmt.Sprintf("field=%s decision=error", field)
	default:
		return fmt.Sprintf("field=%s decision=allow", field)
	}
}

func secretAuditFieldName(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return aictx.ArgString(args, "field")
}
