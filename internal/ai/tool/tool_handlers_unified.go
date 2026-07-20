package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// handleExec 按资产真实类型派发命令执行，取代 14 个按类型区分的专用工具
// （run_command/exec_sql/exec_redis/exec_k8s/...）。
//
// 执行顺序是有意为之，不能重排：
//  1. 解析资产（assetref.Resolve）——错误直接返回，无副作用。
//  2. 执行器查找：类型未注册执行器（Plan A 尚不支持的类型）→ 明确的 unsupported 错误。
//  3. 门禁：该资产类型的用法文档是否已到过模型面前；未到过则返回引导文本
//     （非 Go error，让模型能在同一轮里自纠：调用 help 后重试），而不是让整轮
//     因 error 中断。
//  4. 规范化（如该类型注册了 CanonicalizeFunc）：把模型给的原始命令改写成
//     "真正会被执行、也应被策略匹配"的形式（目前只有 k8s，注入 --context/--namespace），
//     结果只用于下一步的权限检查，不覆盖原始命令。
//  5. 权限检查：用规范化后的命令字符串做检查——批的是这个，就该按这个匹配策略/审计。
//  6. 执行：用原始命令，不是规范化后的字符串。规范化命令是给策略匹配/审批弹窗/审计展示
//     用的形式（例如 k8s 的 EffectiveCommand 是未加引号的 "kubectl " + strings.Join(args,
//     " ")），执行器（如 helper.ExecK8sOnAsset）会对原始命令重新解析一次；把展示形式喂给
//     它是有损的（引号、内部空格都会被吃掉），批准的命令就和实际执行的命令对不上了。
//
// 执行器查找与门禁必须排在权限检查之前：CheckForAsset 有用户可见副作用
// （NeedConfirm 会弹审批对话框并阻塞等待用户响应）。若把权限检查提前，模型对一个
// 压根不支持、或用法未知的类型调 exec 时，用户会先被弹一次审批，批准之后命令却因
// 查不到执行器/撞上门禁而根本不执行——所以无副作用的判断必须全部走完，才能碰有副作用的那一步。
func handleExec(ctx context.Context, args map[string]any) (string, error) {
	asset, err := assetref.Resolve(ctx, aictx.ArgString(args, "asset"))
	if err != nil {
		return "", err
	}

	command := aictx.ArgString(args, "command")
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("missing required parameter: command")
	}

	exec, ok := permission.ExecutorFor(asset.Type)
	if !ok {
		return "", unsupportedTypeError(asset)
	}

	if gate := GetDocGate(ctx); gate != nil {
		convID := aictx.GetConversationID(ctx)
		if !gate.IsDocumented(convID, asset.Type) {
			return execGuidance(asset), nil
		}
	}

	checkCommand := command
	if canonicalize, ok := permission.CanonicalizeFor(asset.Type); ok {
		canonicalCommand, err := canonicalize(asset, command)
		if err != nil {
			return "", err
		}
		checkCommand = canonicalCommand
	}

	scope := aictx.ArgString(args, "scope")

	if checker := permission.GetPolicyChecker(ctx); checker != nil {
		result := checker.CheckForAsset(ctx, asset.ID, asset.Type, checkCommand)
		aictx.RecordDecision(ctx, result)
		if result.Decision != aictx.Allow {
			return result.Message, nil
		}
	}

	return exec(ctx, asset, command, scope)
}

// execGuidance 门禁未满足时返回的引导文本：点名资产与解析出的类型，指引模型
// 先调 help 再重试 exec（spec §4.6 第 1 条给出的措辞）。返回值而非 error——
// 模型看到这段文本后能在同一轮内自纠。
func execGuidance(asset *asset_entity.Asset) string {
	return fmt.Sprintf(
		"asset %q is type=%s — call help(asset=%q) for its command syntax before using exec.",
		asset.Name, asset.Type, asset.Name)
}

// unsupportedTypeError 类型未注册执行器（当前 Plan A 尚未覆盖，如 mongodb/etcd/kafka）
// 时返回的明确错误。
func unsupportedTypeError(asset *asset_entity.Asset) error {
	return fmt.Errorf("asset %q (type=%s) has no exec support yet; supported types: %s",
		asset.Name, asset.Type, strings.Join(permission.RegisteredExecTypes(), ", "))
}

// handleHelp 返回资产类型的用法文档，并把该类型标记为"该会话已知晓"，供 exec 的
// 门禁检查使用。
func handleHelp(ctx context.Context, args map[string]any) (string, error) {
	asset, err := assetref.Resolve(ctx, aictx.ArgString(args, "asset"))
	if err != nil {
		return "", err
	}

	doc, ok := permission.HelpFor(asset.Type)
	if !ok {
		return "", fmt.Errorf("asset %q (type=%s) has no help documentation yet; supported types: %s",
			asset.Name, asset.Type, strings.Join(permission.RegisteredExecTypes(), ", "))
	}

	if gate := GetDocGate(ctx); gate != nil {
		gate.MarkDocumented(aictx.GetConversationID(ctx), asset.Type)
	}

	return fmt.Sprintf("Asset %q is type=%s.\n\n%s", asset.Name, asset.Type, doc), nil
}
