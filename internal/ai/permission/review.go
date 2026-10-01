package permission

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

// applyReviews 按权限模式就地处理 results 里"需要人确认"的结果，不区分资产类型：
//   - 默认：原样保留，问人；
//   - 辅助审批：模型审核通过就放行，否则仍然问人；
//   - Autopilot：模型审核通过就放行，否则直接拒绝，不等人。
//
// 规则已经放行或拒绝的结果原样保留。需要审核的命令一次交给 ReviewBatch。
// 标为 Unreviewable 的结果和还会从管道读入内容（PipedInput）的命令只能由人判断，不交给
// 模型：辅助审批照常问人，Autopilot 直接拒绝。
// 审核服务没有注册时（未经 bootstrap 的进程）不审核。
func applyReviews(ctx context.Context, reqs []PermissionRequest, results []aictx.CheckResult) {
	reviewer := command_review_svc.Default()
	if reviewer == nil {
		return
	}
	var idx []int
	var modes []string
	var inputs []command_review_svc.Input
	for i, r := range results {
		if r.Decision != aictx.NeedConfirm {
			continue
		}
		mode := resolvePermissionMode(ctx, reqs[i].AssetID)
		if !reviewedMode(mode) {
			continue
		}
		if r.Unreviewable {
			if mode == policyent.PermissionModeAutopilot {
				results[i] = aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceAutopilotDeny, Message: unreviewableDenyMessage(ctx, r.Message)}
			}
			continue
		}
		if reqs[i].PipedInput {
			if mode == policyent.PermissionModeAutopilot {
				results[i] = aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceAutopilotDeny, Message: pipedInputDenyMessage(ctx)}
			}
			continue
		}
		idx = append(idx, i)
		modes = append(modes, mode)
		assetType, syntax := reviewTypeFor(reqs[i].AssetType)
		inputs = append(inputs, command_review_svc.Input{AssetType: assetType, Command: reqs[i].Command, Syntax: syntax})
	}
	if len(inputs) == 0 {
		return
	}
	for k, r := range reviewer.ReviewBatch(ctx, inputs) {
		i := idx[k]
		results[i] = applyReview(ctx, reqs[i], modes[k], results[i], r)
	}
}

// applyReview 把一条审核结果按模式落成权限结果。
func applyReview(ctx context.Context, req PermissionRequest, mode string, result aictx.CheckResult, r command_review_svc.Result) aictx.CheckResult {
	info := r.Info(mode)
	logger.Ctx(ctx).Info("permission review applied",
		zap.Int64("assetID", req.AssetID), zap.String("assetType", req.AssetType),
		zap.String("mode", mode), zap.String("outcome", string(info.Outcome)))

	if r.Outcome == command_review_svc.OutcomePass {
		source := aictx.SourceAssistedAllow
		if mode == policyent.PermissionModeAutopilot {
			source = aictx.SourceAutopilotAllow
		}
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: source, Review: info}
	}
	if mode == policyent.PermissionModeAssisted {
		result.Review = info
		return result
	}
	return aictx.CheckResult{
		Decision:       aictx.Deny,
		DecisionSource: aictx.SourceAutopilotDeny,
		Message:        autopilotDenyMessage(ctx, info),
		Review:         info,
	}
}

// reviewedMode 判断这种权限模式下，原本要问人的命令会不会先交给模型审核。
func reviewedMode(mode string) bool {
	return mode == policyent.PermissionModeAssisted || mode == policyent.PermissionModeAutopilot
}

// ReviewsCommands 判断这台资产上原本要问人的命令会不会交给模型审核：生效的权限模式需要审核，
// 并且审核服务已注册。opsctl exec 据此决定要不要先看一眼管道里有没有内容（见 PipedInput）。
func ReviewsCommands(ctx context.Context, assetID int64) bool {
	return command_review_svc.Default() != nil && reviewedMode(resolvePermissionMode(ctx, assetID))
}

// resolvePermissionMode 取生效的权限模式：资产自己的设置优先，没设置时沿分组链向上找
// 第一个设置了的；都没有就是默认。资产读取失败时按默认处理（问人），不会因此放宽。
func resolvePermissionMode(ctx context.Context, assetID int64) string {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		logger.Ctx(ctx).Warn("get asset for permission mode", zap.Int64("assetID", assetID), zap.Error(err))
		return policyent.PermissionModeDefault
	}
	return permissionModeOf(ctx, asset)
}

// splitAutopilotGrants 把授权申请里 Autopilot 资产的部分分出来，返回其余部分和这些资产的名字。
// Autopilot 是无人值守，不会有人来批；批准的又是通配模式，会让之后匹配的命令跳过逐条审核，
// 所以这些资产不接受授权申请，由调用方直接执行、模型逐条审核。
func splitAutopilotGrants(ctx context.Context, items []GrantItem) (rest []GrantItem, autopilot []string) {
	for _, item := range items {
		if item.AssetID > 0 {
			asset, err := asset_svc.Asset().Get(ctx, item.AssetID)
			if err != nil {
				logger.Ctx(ctx).Warn("get asset for grant permission mode", zap.Int64("assetID", item.AssetID), zap.Error(err))
			} else if permissionModeOf(ctx, asset) == policyent.PermissionModeAutopilot {
				autopilot = append(autopilot, asset.Name)
				continue
			}
		}
		rest = append(rest, item)
	}
	return rest, autopilot
}

// autopilotGrantMessage 告诉调用方这些资产不走授权申请，该怎么做。
func autopilotGrantMessage(ctx context.Context, assets []string) string {
	return policy.PolicyFmt(ctx,
		"Asset(s) %s use Autopilot: grant requests are not accepted there and not needed, and nothing was granted for them. Run the commands directly; each one is reviewed by the model. Do not use a grant request to get around a command the review refused; leave that command to the user.",
		"资产 %s 使用 Autopilot：不接受授权申请，也不需要，没有为它授予任何权限。请直接执行命令，每条命令会由模型单独审核；被审核拒绝的命令不要改用授权申请绕过，交给用户处理。",
		strings.Join(assets, ", "))
}

// permissionModeOf 是 resolvePermissionMode 在已经拿到资产时的版本。
func permissionModeOf(ctx context.Context, asset *asset_entity.Asset) string {
	if asset.PermissionMode != policyent.PermissionModeInherit {
		return asset.PermissionMode
	}
	for _, g := range policy.ResolveGroupChain(ctx, asset.GroupID) {
		if g.PermissionMode != policyent.PermissionModeInherit {
			return g.PermissionMode
		}
	}
	return policyent.PermissionModeDefault
}

// autopilotDenyMessage 是 Autopilot 拒绝时返回给调用方的原因，调用方的 agent 据此决定下一步。
// 审核失败时的下一步见 failReasons。
func autopilotDenyMessage(ctx context.Context, info *aictx.ReviewInfo) string {
	summary := ReviewSummary(ctx, info)
	if info.Outcome == aictx.ReviewReject {
		return policy.PolicyFmt(ctx,
			"%s; the command was not executed. Do not retry it as-is: take a safer approach, or leave it for the user to run manually.",
			"%s，命令没有执行。不要原样重试，换一个更安全的做法，或者留给用户手动执行。",
			summary)
	}
	next := failReasonOf(info.Reason).next
	return policy.PolicyFmt(ctx, "%s; the command was not executed. %s", "%s，命令没有执行。%s",
		summary, policy.PolicyMsg(ctx, next.en, next.zh))
}

// unreviewableDenyMessage 是 Autopilot 拒绝一条只能由人判断的命令时返回给调用方的原因，
// reason 是规则层给出的说明（如"策略无法逐条校验其中的子命令，请修正命令语法后重试"）。
func unreviewableDenyMessage(ctx context.Context, reason string) string {
	return policy.PolicyFmt(ctx,
		"%s. Autopilot does not send a command the policy cannot check one sub-command at a time to the model review; the command was not executed.",
		"%s。Autopilot 不会把无法逐条检查的命令交给模型审核，命令没有执行。",
		reason)
}

// pipedInputDenyMessage 是 Autopilot 拒绝一条还会从管道读入内容的命令时返回给调用方的原因。
func pipedInputDenyMessage(ctx context.Context) string {
	return policy.PolicyMsg(ctx,
		"The command also reads piped input, which the model review cannot see, so Autopilot does not run it; the command was not executed. If nothing needs to be piped in, redirect stdin from /dev/null (< /dev/null); to upload a file, use opsctl cp; otherwise leave the command to the user.",
		"命令还会从管道读入内容，模型审核看不到这部分，Autopilot 不会执行，命令没有执行。没有要传的内容就把 stdin 重定向到 /dev/null（< /dev/null）；要上传文件用 opsctl cp；否则把命令留给用户处理。")
}

// reviewTypeFor 返回交给模型审核的资产类型和命令写法。调用方可能传别名（opsctl exec 传的是
// 审批类型 exec、sql），这里统一换成注册时的规范类型：模型看到的是真实的资产类型，缓存键也
// 不会因入口不同而分成几份。没有注册的类型（扩展类型）原样使用，按纯文本处理。
func reviewTypeFor(assetType string) (string, command_review_svc.Syntax) {
	if handler, ok := permissionTypeFor(assetType); ok {
		return handler.canonical, handler.reviewSyntax
	}
	return assetType, command_review_svc.SyntaxText
}

// ReviewSummary 把审核结果写成一句话，给审批提示和拒绝信息用，例如"模型审核未通过（可能中断服务）"。
func ReviewSummary(ctx context.Context, info *aictx.ReviewInfo) string {
	switch info.Outcome {
	case aictx.ReviewPass:
		return policy.PolicyMsg(ctx, "Model review passed", "模型审核通过")
	case aictx.ReviewReject:
		labels := make([]string, 0, len(info.Failed))
		for _, q := range info.Failed {
			labels = append(labels, reviewQuestionLabel(ctx, q))
		}
		return policy.PolicyFmt(ctx, "Model review did not pass (%s)", "模型审核未通过（%s）",
			strings.Join(labels, policy.PolicyMsg(ctx, ", ", "、")))
	default:
		return policy.PolicyFmt(ctx, "Model review failed (%s)", "模型审核失败（%s）", reviewFailReason(ctx, info.Reason))
	}
}

func reviewQuestionLabel(ctx context.Context, question string) string {
	switch question {
	case command_review_svc.QuestionDestructive:
		return policy.PolicyMsg(ctx, "may destroy or irreversibly change data", "可能破坏或不可恢复地修改数据")
	case command_review_svc.QuestionDisruptive:
		return policy.PolicyMsg(ctx, "may interrupt services", "可能中断服务")
	case command_review_svc.QuestionRemoteCode:
		return policy.PolicyMsg(ctx, "downloads and runs code", "会下载并执行代码")
	default:
		return question
	}
}

func reviewFailReason(ctx context.Context, reason string) string {
	label := failReasonOf(reason).label
	return policy.PolicyMsg(ctx, label.en, label.zh)
}

// bilingual 是一句中英对照的话，按调用方的语言取（policy.PolicyMsg）。
type bilingual struct{ en, zh string }

// failReasonText 是一种审核失败原因的说明，以及 Autopilot 拒绝时告诉调用方的下一步。
type failReasonText struct{ label, next bilingual }

var (
	retryLater = bilingual{"You may retry later.", "可以稍后重试。"}
	// fixSettings 用在只有用户改设置才能恢复的原因上：原样重试结果一样。
	fixSettings = bilingual{
		"Retrying will not help until the user fixes the command review settings; leave the command to the user.",
		"用户修正命令审核的设置之前重试也不会成功，把命令留给用户处理。",
	}
)

// failReasons 是各种审核失败原因的说明和下一步。只有临时性的原因（超时、服务不可用）才让
// 调用方稍后重试；命令过长、无法解析和设置问题原样重试结果一样，要告诉它该怎么改，或者留给用户。
var failReasons = map[string]failReasonText{
	command_review_svc.ReasonNotConfigured:    {bilingual{"the command review API key is not configured", "未配置命令审核的 API key"}, fixSettings},
	command_review_svc.ReasonAPIKeyUnreadable: {bilingual{"the saved command review API key cannot be read", "已保存的命令审核 API key 无法读取"}, fixSettings},
	command_review_svc.ReasonInvalidAPIKey:    {bilingual{"the command review API key is invalid", "命令审核的 API key 无效"}, fixSettings},
	command_review_svc.ReasonTimeout:          {bilingual{"timed out", "超时"}, retryLater},
	command_review_svc.ReasonUnavailable:      {bilingual{"service unavailable", "服务不可用"}, retryLater},
	command_review_svc.ReasonTooLong: {bilingual{"command too long", "命令过长"}, bilingual{
		"Retrying it as-is gives the same result: shorten it or split it into several commands.",
		"原样重试结果一样，把命令缩短或拆成几条再执行。",
	}},
	command_review_svc.ReasonUnparseable: {bilingual{"the command cannot be parsed", "命令无法解析"}, bilingual{
		"Retrying it as-is gives the same result: fix the command syntax first.",
		"原样重试结果一样，先修正命令语法。",
	}},
	command_review_svc.ReasonUndecodable: {bilingual{"the command hides the code it runs behind decoding", "命令把要执行的内容藏在解码后面"}, bilingual{
		"Retrying it as-is gives the same result: send the script itself instead of wrapping it so it can be reviewed, or leave the command to the user.",
		"原样重试结果一样，把要执行的脚本直接写出来再送审，或者把命令留给用户处理。",
	}},
}

// failReasonOf 返回失败原因的说明；不认识的原因按服务不可用处理，和前端一致。
func failReasonOf(reason string) failReasonText {
	if t, ok := failReasons[reason]; ok {
		return t
	}
	return failReasons[command_review_svc.ReasonUnavailable]
}
