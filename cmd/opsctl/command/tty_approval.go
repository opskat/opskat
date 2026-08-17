package command

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/repository/asset_repo"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// ttyChoice 是终端审批的决策值。刻意不复用 permission.ApprovalResponse 的
// allowAll：决策 13 规定“永久允许”走 opsctl policy allow 的规则写入路径、不产生
// grant 行，因此提示产出的决策值里不含 allowAll，只有它自己的三态。
type ttyChoice uint8

const (
	ttyDeny        ttyChoice = iota // 空值即拒绝，遗漏分支天然 fail-closed
	ttyAllowOnce                    // 本次允许
	ttyAllowAlways                  // 永久允许（写一条规则，决策 13）
)

// errTTYApprovalRetry 表示输入不在当前 ApprovalKind 的白名单里，调用方重新提示；
// 不静默当成允许，也不静默当成拒绝。
var errTTYApprovalRetry = errors.New("unrecognized approval choice")

// parseTTYApprovalInput 把一行终端输入解析成决策（spec Testing decisions：kind × 输入）。
// 行尾换行与首尾空白被容忍；空输入（直接回车）判为拒绝；白名单精确匹配小写字母，
// 不做大小写折叠——与 permission.ParseApprovalResponse 同一从严标准，避免大小写
// 漂移被解释成授权。“p”（永久允许）只在 single 上合法，once/batch/delete/extension
// 不提供。
func parseTTYApprovalInput(kind, input string) (ttyChoice, error) {
	switch strings.TrimSpace(input) {
	case "", "d":
		return ttyDeny, nil
	case "a":
		return ttyAllowOnce, nil
	case "p":
		if kind == permission.ApprovalKindSingle {
			return ttyAllowAlways, nil
		}
	}
	return ttyDeny, errTTYApprovalRetry
}

// ttyApprovalKind 把审批类型映射到 ApprovalKind，决定终端提示给几个选项。
// 与桌面端 internal/app/opsctl.singleApprovalKind 是同一条映射；不能直接复用后者，
// 因为它所在的包绑着 wails 运行时，CLI 不该拖进来。两边若要改，一起改。
func ttyApprovalKind(approvalType string) string {
	switch approvalType {
	case permission.ApprovalTypeDelete:
		return permission.ApprovalKindDelete
	case "ext_tool":
		return permission.ApprovalKindExtension
	default:
		if permission.SupportsGrantApproval(approvalType) {
			return permission.ApprovalKindSingle
		}
		return permission.ApprovalKindOnce
	}
}

// writeAllowAlwaysRule 是“永久允许”的规则写入接缝（决策 13）：与 opsctl policy allow
// 共用同一条写入路径——先写规则、写成功才放行本次操作，不产生 grant 行。落点函数由
// T5（internal/ai/permission 的按类型落点 + opsctl policy allow）交付并接到这里；
// 接缝之前的默认实现返回错误，选择“永久允许”会如实失败并按拒绝处理（规则没写成功
// 就不放行）。T5 的实现负责它那侧的回显、二次确认、更宽标注与 deny 遮蔽检测（决策 12、19）。
var writeAllowAlwaysRule = func(_ context.Context, _ int64, _ string, _ []string) error {
	return errors.New("writing a permanent rule is not wired yet; choose allow once or deny, or run 'opsctl policy allow'")
}

// terminalApprovalStreams 是终端审批的输入输出来源：提示写 stderr、决策读 stdin，
// stdout 只承载命令自身的输出。变量化以便测试注入缓冲。
var terminalApprovalStreams = func() (io.Reader, io.Writer) { return os.Stdin, os.Stderr }

// envPolicyLangCtx 为没有 context 可传的审批缝（requireBatchApproval 的注入签名
// 固定为 (items, session)）重建带策略语言的 context，与 root.go 用同一个解析器。
func envPolicyLangCtx() context.Context {
	return aictx.WithPolicyLang(context.Background(), resolvePolicyLang(
		os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")))
}

// normalizedApprovalSubjects 给出经 NormalizeGrantPatterns 归一化之后的主体：
// 终端提示展示它、审计的 MatchedPattern 记它——人必须看到自己真正授出的范围，
// 而不是自己敲的原串（复合命令按子命令拆、OSS 的 DSL 派生策略串都发生在这里）。
// 主体是系统替用户推导的，origin 恒为 GrantOriginSystem。
func normalizedApprovalSubjects(approvalType, command string) []string {
	return permission.NormalizeGrantPatterns(approvalType, command, permission.GrantOriginSystem)
}

// ttyApprovalFace 返回这次审批的方向化审批面：CheckType 优先（cp 的单端点审批由
// cp.go 携带 cp:read/cp:write），回落 req.Type。归一化、提示展示、照抄命令与
// “永久允许”的规则落点都按它取——把方向面折叠成 "cp" 会丢掉方向。
func ttyApprovalFace(req approval.ApprovalRequest) string {
	if req.CheckType != "" {
		return req.CheckType
	}
	return req.Type
}

// writeTerminalApprovalRule 落一条终端“永久允许”规则（决策 13）。非 cp 面经
// writeAllowAlwaysRule 接缝（policy.go 的适配器）；方向化的 cp 面不能走它——那个
// 适配器按资产自身类型选形状，方向面会绕过它的 cp 守卫、把路径写成一条 ssh 命令
// 规则（实测如此）。这里直接以方向面为 canonical 构造目标调 writePermanentRules：
// 它才是“与 opsctl policy allow 共用”的唯一写入路径，回显、二次确认、deny 遮蔽
// 检测与审计一并生效，命中 permission 注册的 cp:read/cp:write 落点。
func writeTerminalApprovalRule(ctx context.Context, req approval.ApprovalRequest, face string, patterns []string) error {
	if face == permission.GrantToolCpRead || face == permission.GrantToolCpWrite {
		asset, err := asset_repo.Asset().Find(ctx, req.AssetID)
		if err != nil {
			return err
		}
		return writePermanentRules(ctx, permission.RuleAllow,
			[]policyWriteTarget{{asset: asset, canonical: face, patterns: patterns}})
	}
	return writeAllowAlwaysRule(ctx, req.AssetID, face, patterns)
}

// runTTYApproval 在终端上完成一次单条审批：提示写 out（stderr）、决策从 in（stdin）
// 读入。空输入（直接回车）、EOF、SIGINT 一律判为拒绝；非白名单输入重新提示。
// 拒绝与允许都以真实决策写回 ApprovalResult（SourceUserDeny / SourceUserAllow），
// 供调用方照常落审计；MatchedPattern 记归一化后的主体。
func runTTYApproval(ctx context.Context, req approval.ApprovalRequest, in io.Reader, out io.Writer) (ApprovalResult, error) {
	face := ttyApprovalFace(req)
	patterns := normalizedApprovalSubjects(face, req.Command)
	kind := ttyApprovalKind(face)
	matched := strings.Join(patterns, ", ")

	log := logger.Ctx(ctx).With(
		zap.String("approvalType", face),
		zap.Int64("assetID", req.AssetID),
		zap.String("sessionID", req.SessionID),
	)
	log.Info("opsctl terminal approval started")

	choice, denyReason := readTTYChoice(ctx, kind, in, out, func() {
		renderTTYApprovalPrompt(ctx, req, face, patterns, kind, out)
	})

	switch choice {
	case ttyAllowAlways:
		// 先写规则、写成功才放行（决策 13）；失败按拒绝收场，错误如实上抛。
		if err := writeTerminalApprovalRule(ctx, req, face, patterns); err != nil {
			log.Error("opsctl terminal approval failed", zap.Error(err))
			return denyTTYApproval(matched, req.SessionID), fmt.Errorf("allow always failed: %w", err)
		}
		log.Info("opsctl terminal approval completed", zap.Bool("approved", true), zap.String("decision", "allow always"))
	case ttyAllowOnce:
		log.Info("opsctl terminal approval completed", zap.Bool("approved", true), zap.String("decision", "allow once"))
	default:
		log.Info("opsctl terminal approval completed", zap.Bool("approved", false), zap.String("reason", denyReason))
		return denyTTYApproval(matched, req.SessionID), fmt.Errorf("operation denied: %s", denyReason)
	}
	return ApprovalResult{
		Decision:       aictx.Allow,
		DecisionSource: aictx.SourceUserAllow,
		MatchedPattern: matched,
		SessionID:      req.SessionID,
	}, nil
}

// denyTTYApproval 是终端拒绝的统一收口：SourceUserDeny + 归一化主体，供审计落库。
func denyTTYApproval(matchedPattern, sessionID string) ApprovalResult {
	return ApprovalResult{
		Decision:       aictx.Deny,
		DecisionSource: aictx.SourceUserDeny,
		MatchedPattern: matchedPattern,
		SessionID:      sessionID,
	}
}

// runTTYBatchApproval 是多端点（opsctl batch / 多源 cp）的终端审批：一次性列出全部
// 条目，整批允许或整批拒绝——与 ApprovalKindBatch 的既有语义一致，不引入逐条选择。
func runTTYBatchApproval(ctx context.Context, items []approval.BatchItem, in io.Reader, out io.Writer) (ApprovalResult, error) {
	log := logger.Ctx(ctx).With(zap.Int("items", len(items)))
	log.Info("opsctl terminal batch approval started")

	choice, denyReason := readTTYChoice(ctx, permission.ApprovalKindBatch, in, out,
		func() { renderTTYBatchPrompt(ctx, items, out) })

	if choice != ttyAllowOnce {
		log.Info("opsctl terminal batch approval completed", zap.Bool("approved", false), zap.String("reason", denyReason))
		return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny},
			fmt.Errorf("operation denied: %s", denyReason)
	}
	log.Info("opsctl terminal batch approval completed", zap.Bool("approved", true))
	return ApprovalResult{
		Decision:       aictx.Allow,
		DecisionSource: aictx.SourceUserAllow,
		MatchedPattern: truncateStr(batchApprovalSubjects(items), 200),
	}, nil
}

// batchApprovalSubjects 汇聚整批的归一化主体，供审计的 MatchedPattern 记录。
func batchApprovalSubjects(items []approval.BatchItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, strings.Join(normalizedApprovalSubjects(item.Type, item.Command), ", "))
	}
	return strings.Join(parts, ", ")
}

// readTTYChoice 反复渲染提示并读取一行输入，直到得到白名单内的决策或一个终止性
// 输入（空行 / EOF / SIGINT 都是拒绝）。SIGINT 只在等待答案期间被捕获，读到后照常
// 按拒绝返回，让调用方写完审计再退出，而不是让进程死在默认信号行为上。
// 返回的 denyReason 描述拒绝的来由（user denied / empty input / EOF / interrupted）。
func readTTYChoice(ctx context.Context, kind string, in io.Reader, out io.Writer, renderPrompt func()) (ttyChoice, string) {
	type lineResult struct {
		text string
		ok   bool
	}

	scanner := bufio.NewScanner(in)
	for {
		renderPrompt()

		lines := make(chan lineResult, 1)
		go func() {
			ok := scanner.Scan()
			lines <- lineResult{text: scanner.Text(), ok: ok}
		}()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		var line lineResult
		select {
		case line = <-lines:
		case <-sigCh:
			signal.Stop(sigCh)
			return ttyDeny, "interrupted"
		}
		signal.Stop(sigCh)

		if !line.ok {
			return ttyDeny, "EOF"
		}
		choice, err := parseTTYApprovalInput(kind, line.text)
		if err != nil {
			fmt.Fprintln(out, policy.PolicyMsg(ctx, "unrecognized input, try again", "无法识别的输入，请重试")) //nolint:errcheck // 终端呈现尽力而为
			continue
		}
		if choice == ttyDeny {
			if strings.TrimSpace(line.text) == "" {
				return ttyDeny, "empty input"
			}
			return ttyDeny, "user denied"
		}
		return choice, ""
	}
}

// renderTTYApprovalPrompt 渲染单条审批的提示：资产名/ID/类型（face，含 cp 方向）、
// 归一化后的主体、审批项自带的 detail，以及按 ApprovalKind 取的选项。给人读的标签
// 跟随策略语言，选项字母恒为 ASCII 小写。
func renderTTYApprovalPrompt(ctx context.Context, req approval.ApprovalRequest, face string, patterns []string, kind string, out io.Writer) {
	fmt.Fprintln(out, policy.PolicyMsg(ctx, "Approval required", "需要审批")) //nolint:errcheck // 终端呈现尽力而为
	if req.AssetName != "" || req.AssetID > 0 {
		fmt.Fprintf(out, "%s %s\n", policy.PolicyMsg(ctx, "Asset:", "资产："), //nolint:errcheck // 终端呈现尽力而为
			assetIdentity(req.AssetName, req.AssetID, face))
	}
	// 无主体的操作（create/update/delete）没有可列的主体，不渲染悬空的标题。
	if len(patterns) > 0 {
		fmt.Fprintf(out, "%s\n", policy.PolicyMsg(ctx, "Subject:", "主体：")) //nolint:errcheck // 终端呈现尽力而为
		for _, p := range patterns {
			fmt.Fprintf(out, "  - %s\n", p) //nolint:errcheck // 终端呈现尽力而为
		}
	}
	if req.Detail != "" {
		fmt.Fprintf(out, "%s %s\n", policy.PolicyMsg(ctx, "Detail:", "详情："), req.Detail) //nolint:errcheck // 终端呈现尽力而为
	}
	fmt.Fprintln(out, ttyOptionsLine(ctx, kind))               //nolint:errcheck // 终端呈现尽力而为
	fmt.Fprint(out, policy.PolicyMsg(ctx, "Choice: ", "请选择：")) //nolint:errcheck // 终端呈现尽力而为
}

// renderTTYBatchPrompt 渲染批量审批的提示：全部条目一次列完，两选。
func renderTTYBatchPrompt(ctx context.Context, items []approval.BatchItem, out io.Writer) {
	fmt.Fprintf(out, "%s\n", policy.PolicyFmt(ctx, //nolint:errcheck // 终端呈现尽力而为
		"Batch approval required (%d items)", "需要批量审批（共 %d 条）", len(items)))
	for i, item := range items {
		fmt.Fprintf(out, "  %d. %s\n", i+1, assetIdentity(item.AssetName, item.AssetID, item.Type)) //nolint:errcheck // 终端呈现尽力而为
		for _, p := range normalizedApprovalSubjects(item.Type, item.Command) {
			fmt.Fprintf(out, "      - %s\n", p) //nolint:errcheck // 终端呈现尽力而为
		}
		if item.Detail != "" {
			fmt.Fprintf(out, "      %s %s\n", policy.PolicyMsg(ctx, "Detail:", "详情："), item.Detail) //nolint:errcheck // 终端呈现尽力而为
		}
	}
	fmt.Fprintln(out, policy.PolicyMsg(ctx, //nolint:errcheck // 终端呈现尽力而为
		"[a] allow all   [d] deny all", "[a] 全部允许   [d] 全部拒绝"))
	fmt.Fprint(out, policy.PolicyMsg(ctx, "Choice: ", "请选择：")) //nolint:errcheck // 终端呈现尽力而为
}

// ttyOptionsLine 按 ApprovalKind 给出选项：single 三选（本次允许/永久允许/拒绝），
// once/batch/delete/extension 两选（本次允许/拒绝）。
func ttyOptionsLine(ctx context.Context, kind string) string {
	if kind == permission.ApprovalKindSingle {
		return policy.PolicyMsg(ctx,
			"[a] allow once   [p] allow always (writes a rule)   [d] deny",
			"[a] 本次允许   [p] 永久允许（写入一条规则）   [d] 拒绝")
	}
	return policy.PolicyMsg(ctx, "[a] allow once   [d] deny", "[a] 本次允许   [d] 拒绝")
}
