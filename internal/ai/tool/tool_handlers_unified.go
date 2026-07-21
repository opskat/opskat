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
//  2. 命令非空校验——command 空或仅空白字符返回错误。
//  3. 执行器查找：类型未注册执行器（Plan A 尚不支持的类型）→ 明确的 unsupported 错误。
//  4. 门禁：该资产类型的用法文档是否已到过模型面前；未到过则返回引导文本
//     （非 Go error，让模型能在同一轮里自纠：调用 help 后重试），而不是让整轮
//     因 error 中断。
//  5. 规范化（如该类型注册了 CanonicalizeFunc）：把模型给的原始命令改写成
//     "真正会被执行、也应被策略匹配"的形式（k8s 注入 --context/--namespace；etcd 走
//     ParseCommand+FormatCommand 的 round trip，规范化大小写/复合命令拼写/flag 顺序），
//     结果只用于下一步的权限检查，不覆盖原始命令。
//  6. 前置条件检查（如该类型注册了 PrecheckFunc）：校验一个跟命令内容无关、但一定会
//     让执行失败的前提条件（目前只有 serial：没有活跃会话）。与规范化一样无副作用，
//     必须排在权限检查之前——原因见下方。
//  7. 权限检查：用规范化后的命令字符串做检查——批的是这个，就该按这个匹配策略/审计。
//  8. 执行：用原始命令，不是规范化后的字符串。规范化命令是给策略匹配/审批弹窗/审计展示
//     用的形式（例如 k8s 的 EffectiveCommand 是未加引号的 "kubectl " + strings.Join(args,
//     " ")），执行器（如 helper.ExecK8sOnAsset）会对原始命令重新解析一次；把展示形式喂给
//     它是有损的（引号、内部空格都会被吃掉），批准的命令就和实际执行的命令对不上了。
//
// 执行器查找、门禁与 precheck 都必须排在权限检查之前：CheckForAsset 有用户可见副作用
// （NeedConfirm 会弹审批对话框并阻塞等待用户响应）。若把权限检查提前，模型对一个
// 压根不支持、用法未知、或前提条件不满足（如没有活跃串口会话）的类型调 exec 时，
// 用户会先被弹一次审批，批准之后命令却因查不到执行器/撞上门禁/precheck 失败而根本
// 不执行——所以无副作用的判断必须全部走完，才能碰有副作用的那一步。
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
		recordShortCircuit(ctx, aictx.SourceExecUnsupportedType)
		return "", unsupportedTypeError(asset)
	}

	if gate := GetDocGate(ctx); gate != nil {
		convID := aictx.GetConversationID(ctx)
		if !gate.IsDocumented(convID, asset.Type) {
			recordShortCircuit(ctx, aictx.SourceExecGateBlocked)
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

	if precheck, ok := permission.PrecheckFor(asset.Type); ok {
		if err := precheck(ctx, asset); err != nil {
			recordShortCircuit(ctx, aictx.SourceExecPrecheckFailed)
			return "", err
		}
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

// recordShortCircuit 把"命令在权限检查之前就被挡下"写进审计决策槽。
//
// 不加审计列，而是复用 RecordDecision 这条既有链路（handler 写 aictx 槽 →
// runner.auditMiddleware 在 c.Next() 之后读出 → audit.ToolCallInfo.Decision →
// audit_logs 的 decision / decision_source 列）：这三条路径的语义跟策略拒绝是同一类
// ——被挡下、没有执行——只是挡下的理由不同，而"理由"正是 DecisionSource 这一列存在
// 的意义。
//
// 不这么做的话，门禁短路会落下一条 success=1、command 完整、decision 为空的审计行：
// 跟一条真正执行成功的行无法区分（策略拒绝的行还有 decision 可认，门禁短路的没有），
// 任何按 success=1 统计"执行过的命令"的审计查询都会把它算进去。
//
// command 列保持原样填充是有意的：知道模型尝试过什么本身有价值，只要这一行同时被
// decision=deny 标成"没有执行"就不会被误读。
func recordShortCircuit(ctx context.Context, source string) {
	aictx.RecordDecision(ctx, aictx.CheckResult{
		Decision:       aictx.Deny,
		DecisionSource: source,
	})
}

// execGuidance 门禁未满足时返回的引导文本：点名资产与解析出的类型，指引模型
// 先调 help 再重试 exec（spec §4.6 第 1 条给出的措辞）。返回值而非 error——
// 模型看到这段文本后能在同一轮内自纠。
func execGuidance(asset *asset_entity.Asset) string {
	return fmt.Sprintf(
		"asset %q is type=%s — call help(asset=%q) for its command syntax before using exec.",
		asset.Name, asset.Type, asset.Name)
}

// unsupportedTypeError 类型未注册执行器（当前 Plan A 尚未覆盖，如 mongodb/kafka）
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
