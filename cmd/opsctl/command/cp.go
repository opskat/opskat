package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
)

// cmdCp 是 opsctl 的传输面：`opsctl cp [-r] <source>... <destination>`，两端各自可以是
// 本地、SSH 或对象存储，九种组合共用 AI 侧同一套端点适配器与同一个 cp 工具
// （spec §6.2 / §6.4 / D3 / D14）。三步与 cmdExec 同构：解析两端 → 逐端点审批 →
// callHandler(ctx, handlers, "cp", …)。
//
// 审批必须由 opsctl 自己走完：cp 工具内部的权限检查对已预检的调用方是豁免的
// （permission.WithPreapproved），因此单文件的具体路径或递归/通配的两端范围都在这里先
// 授权，获批后共享工具才探测目标路径、展开并传输（D17/D18）。唯一例外是 SSH 的 `~`：
// 解析它必须先建立 SFTP 会话查询 home，展开后的绝对路径才会进入审批与后续 I/O。
func cmdCp(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printCpUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	recursive, rest := extractRecursiveFlag(args)
	if len(rest) < 2 {
		printCpUsage()
		return 1
	}

	if session != "" {
		ctx = aictx.WithSessionID(ctx, session)
	}
	ctx = aictx.WithAuditSource(ctx, "opsctl")
	ctx, sftpCache, ownsSFTPCache := helper.EnsureSFTPClientCache(ctx)
	if ownsSFTPCache {
		defer func() { _ = sftpCache.Close() }()
	}
	ctx = tool.WithCpProgressObserver(ctx, func(progress tool.CpProgress) {
		fmt.Fprintf(os.Stderr, "Transferred %d/%d: %s -> %s (%d bytes)\n",
			progress.Completed, progress.Total, progress.Src, progress.Dst, progress.Bytes)
	})

	dst, err := parseCpEndpoint(ctx, rest[len(rest)-1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	srcs := make([]*cpEndpoint, 0, len(rest)-1)
	sourcePaths := make([]string, 0, len(rest)-1)
	for _, raw := range rest[:len(rest)-1] {
		src, srcErr := parseCpEndpoint(ctx, raw)
		if srcErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", srcErr)
			return 1
		}
		if !src.isRemote() && !dst.isRemote() {
			fmt.Fprintln(os.Stderr, "Error: at least one path must be remote (<asset>:/<path>)")
			return 1
		}
		// D12：两端是同一个对象存储资产时对象要下行再上行、绕一圈本地进程。服务端 copy
		// 的能力在 exec 那一面，指过去比在 cp 里再实现一次更诚实；静默流式则会让用户不知道
		// 自己让一个 10 GB 对象走了一趟本机。
		if src.isRemote() && dst.isRemote() && src.asset.ID == dst.asset.ID {
			if hint := helper.SameAssetCopyHint(src.adapter, dst.asset.Name); hint != "" {
				fmt.Fprintf(os.Stderr, "Note: %s\n", hint)
			}
		}
		srcs = append(srcs, src)
		sourcePaths = append(sourcePaths, src.path)
	}

	plan, err := helper.PlanTransfer(sourcePaths, dst.path, recursive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if plan.Multiple {
		return cmdCpMultiSource(ctx, handlers, srcs, dst, recursive)
	}
	return cmdCpSingleSource(ctx, handlers, srcs[0], dst)
}

// cmdCpSingleSource 传一个被指名的文件/对象：目的地就是用户写的那个字符串，没有基点拼接，
// 因此也不要求尾随 "/"。审批排在目标路径探测之前——两端在解析时就已完全确定，先问一句
// "这个路径存在吗"只会把一次不需要授权的探测塞进审批之前。SSH `~` 的 home 查询不探测
// 目标路径，是把审批主体变成确定绝对路径所必需的解析步骤。
func cmdCpSingleSource(
	ctx context.Context, handlers map[string]tool.ToolHandlerFunc, src, dst *cpEndpoint,
) int {
	if err := dst.adapter.ValidateDestination(dst.path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 文件传输与执行命令等价（写 authorized_keys / cron / 被 systemd 引用的脚本都够
	// 换来一次执行），因此必须与 exec 一样过审批。审批放在 proxy 与工具两条路的共同上游，
	// 否则走 proxy 的那条路径会漏。
	approvalCtx, result, approvalAssetID, err := requireCpApproval(ctx, []cpTarget{
		{ep: src, dir: helper.DirRead, path: src.path},
		{ep: dst, dir: helper.DirWrite, path: dst.path},
	}, cpDetail([]*cpEndpoint{src}, dst))
	if err != nil {
		return cpApprovalFailed(approvalCtx, []*cpEndpoint{src}, dst, false, approvalAssetID, err, result)
	}
	ctx = approvalCtx
	decision := result.ToCheckResult()
	params := cpToolParams(src.arg, dst.arg, false, src.assetID(), dst.assetID())

	// proxy 快路径复用桌面端的 SSH 连接池，因此只在远端端点全是 SSH 时启用——对象存储
	// 没有对应能力。它一次只传一个被指名的文件、且服务端不建父目录（sftp.Create），
	// 所以也只用在单源形态上，即完全等价于收敛前的四种组合。
	if proxy := cpSSHProxyClientFn(); proxy != nil && cpAllRemoteSupportPooledProxy(src, dst) {
		exitCode := cmdCpViaProxy(proxy, src, dst)
		var cpErr error
		if exitCode != 0 {
			cpErr = fmt.Errorf("cp via proxy failed with exit code %d", exitCode)
		}
		argsJSON, marshalErr := cpAuditArgs(params)
		if marshalErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", marshalErr)
			return 1
		}
		writeOpsctlAudit(ctx, "cp", argsJSON, fmt.Sprintf(`{"status":"completed","exit_code":%d}`, exitCode), cpErr, decision)
		return exitCode
	}

	return callHandler(ctx, handlers, "cp", params, decision)
}

// cmdCpMultiSource 处理多源形态：recursive 为真、源路径含 glob 元字符、或给了多个源
// （spec §6.5）。判的是**形态**不是命中条数——一个只命中一个文件的 glob 仍然是多源，
// 它的落点是目的基点 + RelPath 而不是字面 dst。
//
// 递归/通配审批源与目的范围，明确列出的多个文件审批各自具体路径；全部通过后才调用共享
// 工具连接、展开并传输（D17/D18）。
func cmdCpMultiSource(
	ctx context.Context, handlers map[string]tool.ToolHandlerFunc,
	srcs []*cpEndpoint, dst *cpEndpoint, recursive bool,
) int {
	detail := cpDetail(srcs, dst)

	// 递归/通配直接审批源范围与目的范围；在审批之前不探测或枚举目标路径。SSH `~` 会先
	// 查询 SFTP home，除此之外不读取远端元数据。明确列出的多个文件仍逐个审批，因为它们的
	// 完整路径已经由用户给出，不需要扫描。
	subjects := make([]cpSubject, 0, 2*len(srcs))
	seen := make(map[cpSubject]bool, 2*len(srcs))
	plans := make([]cpTransferPlan, 0, len(srcs))
	for _, src := range srcs {
		if cpSourceExpands(src, recursive) {
			subjects = appendCpSubject(subjects, seen, src, src.path, helper.DirReadScope)
			subjects = appendCpSubject(subjects, seen, dst, dst.path, helper.DirWriteScope)
			plans = append(plans, cpTransferPlanFor(src, dst, nil, recursive))
			continue
		}
		listing := cpNamedListing(src.path)
		for _, entry := range listing.Entries {
			dstPath := dst.path + entry.RelPath
			// 形态校验排在这次传输的审批项之前：目的端不经过 List，ApprovalSubject 又不能
			// 报错，少了这一关，一个形态错误的目的地会带着错误的主体走完审批、到写入才失败。
			if err := dst.adapter.ValidateDestination(dstPath); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				return 1
			}
			subjects = appendCpSubject(subjects, seen, src, entry.Path, helper.DirRead)
			subjects = appendCpSubject(subjects, seen, dst, dstPath, helper.DirWrite)
		}
		plans = append(plans, cpTransferPlanFor(src, dst, listing, recursive))
	}

	batchResult, err := cpBatchApprovalFn(ctx, subjects, detail)
	if batchResult.SessionID != "" {
		ctx = aictx.WithSessionID(ctx, batchResult.SessionID)
	}
	if err != nil {
		return cpApprovalFailed(ctx, srcs, dst, recursive, 0, err, batchResult)
	}
	decision := batchResult.ToCheckResult()

	// 快速失败：任一条源出错立即中止（D19）。不采用 POSIX cp 的"继续并最终非零"——每个已
	// 传输的字节都是一次已批准的副作用，出意外后继续会留下一个看起来完整、实际残缺的
	// 目的地，而"残缺"这件事从目的端是看不出来的。
	for i, plan := range plans {
		params := cpToolParams(plan.src.arg, plan.dstArg, recursive, plan.src.assetID(), dst.assetID())
		exitCode := callHandler(ctx, handlers, "cp", params, decision)
		if exitCode != 0 {
			if len(plans) > 1 {
				fmt.Fprintf(os.Stderr, "Error: transferred %d/%d sources, then %q failed\n", i, len(plans), plan.src.raw)
			}
			return exitCode
		}
	}
	return 0
}

// cpTransferPlan 是一条源参数的传输计划：交给 cp 工具的目的端串按源的形态取——枚举形态的
// 源交出目的基点，由工具按 RelPath 拼落点；被指名的单个文件交出拼好的具体落点，因为工具
// 的单源形态写的就是 dst 字面量。
type cpTransferPlan struct {
	src    *cpEndpoint
	dstArg string
}

// cpTransferPlanFor 按源的形态定这条源怎么交给工具：枚举形态交出目的基点，明确文件交出
// 已经按 basename 拼好的具体落点。
//
// 被指名的源恒展开成一条——那一条由 cpNamedListing 直接给出，不经过 List——拼出来的落点
// 与它自己那条审批主体逐字相同。它若其实是个目录/前缀，报错的是工具（TransferAdapter.List
// 的"指名一个目录却没有 recursive"那一支），排在这次读被批准之后。
func cpTransferPlanFor(src, dst *cpEndpoint, listing *helper.ListResult, recursive bool) cpTransferPlan {
	if cpSourceExpands(src, recursive) {
		return cpTransferPlan{src: src, dstArg: dst.arg}
	}
	return cpTransferPlan{src: src, dstArg: dst.argFor(dst.path + listing.Entries[0].RelPath)}
}

// cpSourceExpands 报告这一条源是不是**枚举**形态：recursive 为真，或路径含 glob 元字符。
// 判的是形态不是命中条数——一个只命中一个文件的 glob 仍然是枚举形态。与 AI 侧 handleCp 的
// 多源形态判定同一条（spec §6.5「何时算多源」），两条入口因此在同一件事上给同一个答案。
func cpSourceExpands(src *cpEndpoint, recursive bool) bool {
	return recursive || helper.HasGlobPattern(src.path)
}

// cpNamedListing 是一条**被指名的**源的展开结果：它自己那一条。指名的源不枚举任何东西，
// 所以这条清单在本地就算得出来——三个适配器的指名分支返回的正是
// Entry{Path: 用户写的那条路径, RelPath: 它的 basename}（transfer_local.go / transfer_ssh.go
// 的 Stat 分支、transfer_oss.go 的 StatObject 分支）。
//
// 在本地算而不是去 List，是因为这条路上没有任何授权：cpSourceExpands 为假时不索取展开
// 授权（D18 的作用域），于是一次 List 就成了未经授权、未经审计的远端探测——SSH 上是一次
// 带认证的 SFTP 会话 + Stat，对象存储上是一次 StatObject，两者都回答了"这条路径在不在、
// 多大"。cmdCpSingleSource 对完全相同的形态早就是这个答复（见那里的注释），两条路因此
// 一致：被指名的路径在解析时就已完全确定，动手之前该发生的只有审批。
//
// Size 留零：这份单条结果只用于计算明确文件的目的 basename，也不进审批主体——主体是路径，
// 不是大小。
func cpNamedListing(path string) *helper.ListResult {
	// filepath.Base 而非 path.Base：本地端的路径在 Windows 上是 `\` 分隔的绝对路径，
	// 而远端路径恒为 `/` 分隔——filepath 在 Windows 上两种分隔符都认，在 Unix 上本来就
	// 只有一种，一个函数覆盖两端。
	return &helper.ListResult{Entries: []helper.Entry{{Path: path, RelPath: filepath.Base(path)}}}
}

// cpEndpoint 是解析后的 cp 的一端：资产（本地端为 nil）、该端点上的路径，以及它的适配器。
type cpEndpoint struct {
	// raw 是用户写的原文，只用于展示（审批弹窗的 detail、错误信息）。
	raw string
	// arg 是交给 cp 工具的端点串：远端一律写成 <数字 id>:<path>，本地是绝对路径。
	arg     string
	asset   *asset_entity.Asset
	path    string
	adapter helper.TransferAdapter
}

// isRemote 报告这一端有没有资产。**有资产才有审批主体**——本地端点没有资产，正确的调用方
// 压根不会去问它要主体（见 helper.TransferAdapter.ApprovalSubject 的文档注释）。
func (e *cpEndpoint) isRemote() bool { return e.asset != nil }

func (e *cpEndpoint) assetID() int64 {
	if e.asset == nil {
		return 0
	}
	return e.asset.ID
}

// argFor 把这一端点上的另一条路径写成工具认得的端点串。
func (e *cpEndpoint) argFor(path string) string {
	if e.asset == nil {
		return path
	}
	return strconv.FormatInt(e.asset.ID, 10) + ":" + path
}

// parseCpEndpoint 把一个端点串解析成 (资产, 路径, 适配器)。语法由 helper.ParseTransferEndpoint
// 定义、两个入口共用，ref 的解析由入口注入：opsctl 侧是 resolveAsset，只有它支持
// "组路径/名称" 消歧。
//
// 交给工具的端点串因此要换成数字 id：工具那一侧走的是 assetref.Resolve（数字 id 或精确
// 名称），组路径它解析不出来，同名资产的消歧规则也与这里不同——原样透传会让"批准的资产"
// 与"被传输的资产"有可能不是同一个。
func parseCpEndpoint(ctx context.Context, raw string) (*cpEndpoint, error) {
	asset, path, err := helper.ParseTransferEndpoint(ctx, raw, resolveAsset)
	if err != nil {
		return nil, err
	}
	adapter, err := helper.TransferAdapterFor(asset)
	if err != nil {
		return nil, err
	}
	path, err = helper.ResolveTransferPath(ctx, adapter, asset, path)
	if err != nil {
		return nil, err
	}
	ep := &cpEndpoint{raw: raw, asset: asset, path: path, adapter: adapter}
	if asset == nil {
		// 相对路径在入口层展开成绝对路径：`opsctl cp ./a.txt` 的手感不变，而工具那一侧
		// 要求本地路径绝对（D11）——它的工作目录不是用户的终端目录。
		abs, absErr := absLocalPath(path)
		if absErr != nil {
			return nil, absErr
		}
		ep.path = abs
	}
	ep.path = helper.NormalizeTransferPath(adapter, ep.path)
	ep.arg = ep.argFor(ep.path)
	return ep, nil
}

// absLocalPath 展开本地路径，并保住尾随的分隔符：filepath.Abs 会 Clean 掉它，而目的地的
// 尾随 "/" 是 D16 的语义（多源落点的基点），丢了它这条命令的含义就变了。
func absLocalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving local path %q: %w", path, err)
	}
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, string(filepath.Separator)) {
		abs += "/"
	}
	return abs, nil
}

// cpAllRemoteSupportPooledProxy 只询问适配器能力，不在共享编排里判断协议类型。
func cpAllRemoteSupportPooledProxy(eps ...*cpEndpoint) bool {
	for _, ep := range eps {
		if ep.isRemote() && !helper.SupportsPooledProxyCopy(ep.adapter) {
			return false
		}
	}
	return true
}

// extractRecursiveFlag 取出 -r / --recursive，返回 (recursive, 其余参数)。cp 的其余参数
// 全是路径，因此不需要 "--" 分隔符那套（exec 才需要，它后面跟的是远端命令）。
func extractRecursiveFlag(args []string) (bool, []string) {
	recursive := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "-r" || arg == "--recursive" {
			recursive = true
			continue
		}
		rest = append(rest, arg)
	}
	return recursive, rest
}

// cpTarget 是一次审批的对象：某个端点上的某条路径，在某个方向上。
type cpTarget struct {
	ep   *cpEndpoint
	dir  helper.Direction
	path string
}

// requireCpApproval 为一次传输发起审批，主体向端点自己的适配器索取（SSH 是 cp + 远端路径，
// 对象存储是 oss + `object.read/write <bucket>/<key>`）——本地路径不属于任何资产，
// 塞进 pattern 无法匹配，因此本地端点不产生审批项。
//
// 多个远端端点要逐个审：先源端读、再目的端写，任一被拒即整体失败（D13）。返回的 ctx 带上
// 审批分配的 sessionID，assetID 是最后审批或拒绝的远端资产，供审计归组和展示。
func requireCpApproval(
	ctx context.Context, targets []cpTarget, detail string,
) (context.Context, ApprovalResult, int64, error) {
	var last ApprovalResult
	lastAssetID := int64(0)
	for _, tg := range targets {
		if !tg.ep.isRemote() {
			continue
		}
		for _, target := range helper.ApprovalSubjectsFor(tg.ep.adapter, tg.path, tg.dir) {
			result, err := cpApprovalFn(ctx, approval.ApprovalRequest{
				Type:      permission.ApprovalTypeFor(target.ApprovalType),
				CheckType: target.ApprovalType,
				AssetID:   tg.ep.asset.ID,
				AssetName: tg.ep.asset.Name,
				Command:   target.Subject,
				Detail:    detail,
				SessionID: aictx.GetSessionID(ctx),
			})
			last = result
			lastAssetID = tg.ep.asset.ID
			if result.SessionID != "" {
				ctx = aictx.WithSessionID(ctx, result.SessionID)
			}
			if err != nil {
				return ctx, result, tg.ep.asset.ID, err
			}
		}
	}
	return ctx, last, lastAssetID, nil
}

// appendCpSubject 追加一条待授权主体。同一条主体只留一条：源与目的落在同一个前缀上时读写
// 主体逐字相同，重复条目让用户在同一份清单里读两遍同一句话。
func appendCpSubject(
	subjects []cpSubject, seen map[cpSubject]bool, ep *cpEndpoint, path string, dir helper.Direction,
) []cpSubject {
	if !ep.isRemote() {
		return subjects
	}
	for _, target := range helper.ApprovalSubjectsFor(ep.adapter, path, dir) {
		subject := cpSubject{
			approvalType: target.ApprovalType,
			assetID:      ep.asset.ID,
			assetName:    ep.asset.Name,
			command:      target.Subject,
		}
		if seen[subject] {
			continue
		}
		seen[subject] = true
		subjects = append(subjects, subject)
	}
	return subjects
}

// cpSubject 是一条待授权的端点访问：某个端点上的某条**具体**路径在某个方向上的主体。
type cpSubject struct {
	approvalType string
	assetID      int64
	assetName    string
	command      string
}

// cpApprovalFn 是 cp 的审批入口。用变量而非直接调用是为了可测——与本包的
// opsctlAuditWriter 同一套路：测试替换掉它，避免真的去连桌面端审批 socket。
var cpApprovalFn = requireApproval

// cpBatchApprovalFn 是多源 cp 的批量审批入口，同上一套路。
var cpBatchApprovalFn = requireCpBatchApproval

// cpBatchSendFn 是 requireCpBatchApproval 把展开出的 items 交给桌面端的最后一步，同上一套路：
// 测试替换它以观察真正要发出的 items（含 Detail），不用连真实的审批 socket。
var cpBatchSendFn = requireBatchApproval

// cpSSHProxyClientFn 允许单测显式关闭 proxy 探测，避免读取默认用户数据目录下的 socket/token。
var cpSSHProxyClientFn = getSSHProxyClient

// requireCpBatchApproval 让本次传输的远端范围过授权；递归/通配提交目录或对象前缀，
// 明确列出的多个文件提交各自的具体路径。
//
// detail 是这次传输的"从哪到哪"（cpDetail），原样搭在每一条范围 item 上，供审批弹窗
// 同时展示另一端；单端点那条路（requireCpApproval）也使用同一字段。
//
// 批量审批没有"始终允许"（ApprovalKindBatch 只允许整批本次放行或拒绝），所以多源 cp
// 不落常驻 grant，重跑要重新批准全部条目——这是 §6.5【实施期更正】裁定接受的缺口。
func requireCpBatchApproval(ctx context.Context, subjects []cpSubject, detail string) (ApprovalResult, error) {
	session := aictx.GetSessionID(ctx)
	items := make([]approval.BatchItem, 0, len(subjects))
	var allowed aictx.CheckResult
	for _, subject := range subjects {
		result := permission.CheckPermission(ctx, subject.approvalType, subject.assetID, subject.command)
		switch result.Decision {
		case aictx.Deny:
			return ApprovalResult{
				Decision:       aictx.Deny,
				DecisionSource: result.DecisionSource,
				MatchedPattern: result.MatchedPattern,
				SessionID:      session,
			}, fmt.Errorf("transfer denied by policy: %s", result.Message)
		case aictx.Allow:
			allowed = result
		default:
			// Type 取原始审批面而非 ApprovalTypeFor 的折叠值：cp:read/cp:write 的方向
			// 要跟着条目走（桌面弹窗的 TypeBadge 不认识的标签按原样展示），结构化拒绝
			// 的照抄命令与终端批量提示按它给 --type cp:read/cp:write。
			items = append(items, approval.BatchItem{
				Type:      subject.approvalType,
				AssetID:   subject.assetID,
				AssetName: subject.assetName,
				Command:   subject.command,
				Detail:    detail,
			})
		}
	}
	if len(items) == 0 {
		return ApprovalResult{
			Decision:       aictx.Allow,
			DecisionSource: allowed.DecisionSource,
			MatchedPattern: allowed.MatchedPattern,
			SessionID:      session,
		}, nil
	}
	return cpBatchSendFn(items, session)
}

// cpToolParams 是交给 cp 工具的参数，同时也是这次调用落进 audit_logs.request 的原文
// （callHandler 把 params 原样 marshal）。src / dst / recursive 是工具的入参；三个 id 只
// 服务审计：资产归属在 opsctl 这一侧已经解析过，而 audit 的回落链只认 args["asset_id"]
// （package audit 依赖不了 assetref，看不见埋在端点串里的资产）。两端都是资产时记目的端，
// 与 AI 侧那条传输的归属规则同一条。
func cpToolParams(srcArg, dstArg string, recursive bool, srcAssetID, dstAssetID int64) map[string]any {
	primaryAssetID := dstAssetID
	if primaryAssetID == 0 {
		primaryAssetID = srcAssetID
	}
	params := map[string]any{
		"src":       srcArg,
		"dst":       dstArg,
		"recursive": recursive,
		"asset_id":  primaryAssetID,
	}
	if srcAssetID > 0 {
		params["source_asset_id"] = srcAssetID
	}
	if dstAssetID > 0 {
		params["destination_asset_id"] = dstAssetID
	}
	return params
}

func cpAuditArgs(params map[string]any) (string, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("marshal cp audit arguments: %w", err)
	}
	return string(data), nil
}

// cpDetail 是审批弹窗与审计里那句"这次传输是从哪到哪"，用的是用户写的原文——它是用户唯一
// 能认出自己敲的那条命令的地方。
func cpDetail(srcs []*cpEndpoint, dst *cpEndpoint) string {
	raws := make([]string, 0, len(srcs))
	for _, src := range srcs {
		raws = append(raws, src.raw)
	}
	return fmt.Sprintf("opsctl cp %s → %s", strings.Join(raws, " "), dst.raw)
}

// cpApprovalFailed 记一行被拒的审计并报错，返回退出码。一条命令一行：审计的资产列只有
// 一对，多个源共用一行，两端的原文照旧留在 command / request 里。
func cpApprovalFailed(
	ctx context.Context, srcs []*cpEndpoint, dst *cpEndpoint, recursive bool, approvalAssetID int64,
	approvalErr error, result ApprovalResult,
) int {
	srcArgs := make([]string, 0, len(srcs))
	srcAssetID := int64(0)
	for _, src := range srcs {
		srcArgs = append(srcArgs, src.arg)
		if srcAssetID == 0 {
			srcAssetID = src.assetID()
		}
	}
	params := cpToolParams(strings.Join(srcArgs, " "), dst.arg, recursive, srcAssetID, dst.assetID())
	if approvalAssetID > 0 {
		// 被拒的那一端才是这行审计该指向的资产：用户拒的是它。
		params["asset_id"] = approvalAssetID
	}
	argsJSON, marshalErr := cpAuditArgs(params)
	if marshalErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", marshalErr)
		return 1
	}
	writeOpsctlAudit(ctx, "cp", argsJSON, "", approvalErr, result.ToCheckResult())
	// 结构化拒绝（NEEDS AUTHORIZATION）→ 退出码 3，stderr 首行是裸标记；其余保持 1。
	return writeApprovalFailure(os.Stderr, approvalErr)
}

// cmdCpViaProxy 通过 proxy 执行一次单文件传输。至少一端是远端由调用方保证。
func cmdCpViaProxy(proxy *sshpool.Client, src, dst *cpEndpoint) int {
	switch {
	case !src.isRemote():
		// Upload: local -> remote
		f, err := os.Open(src.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		if err := proxy.Upload(sshpool.ProxyRequest{
			AssetID: dst.asset.ID,
			DstPath: dst.path,
		}, f); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case !dst.isRemote():
		// Download: remote -> local
		f, err := os.Create(dst.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		if err := proxy.Download(sshpool.ProxyRequest{
			AssetID: src.asset.ID,
			SrcPath: src.path,
		}, f); err != nil {
			_ = os.Remove(dst.path)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	default:
		// Asset-to-asset transfer: remote -> remote
		if err := proxy.Copy(sshpool.ProxyRequest{
			AssetID:    dst.asset.ID,
			SrcAssetID: src.asset.ID,
			SrcPath:    src.path,
			DstPath:    dst.path,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}
}

func printCpUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl cp [-r] <source>... <destination>

Path Format:
  Local path:      /path/to/file  or  ./relative/path
  SSH server:      <asset>:/<remote-path>          (asset name, ID, or group/name)
  Object storage:  <asset>:/<bucket>/<key>

At least one of source or destination must be on an asset; any combination of
the two sides works, including SSH server to object storage.

Flags:
  -r, --recursive   Transfer a directory tree / object prefix.

Multiple Sources:
  With -r, a glob pattern, or more than one source, the destination must end
  with "/" and each entry lands at <destination>/<path relative to the source
  base>. Quote remote globs so the local shell does not expand them first.
  Symlinks encountered during expansion are skipped and reported.

Approval:
  Every asset endpoint is authorized separately under that asset's own policy,
  before any byte is transferred. Recursive/glob transfers approve the source
  and destination directory/object-prefix scopes before listing their contents.
  An interactive terminal prompts here; otherwise the running desktop app is
  asked, and with neither available opsctl exits with code 3 and a NEEDS
  AUTHORIZATION marker telling you which 'opsctl policy allow' line to run.

Examples:
  opsctl cp ./config.yml web-server:/etc/app/config.yml   Upload by name
  opsctl cp 1:/var/log/app.log ./app.log                  Download by ID
  opsctl cp 1:/etc/hosts 2:/tmp/hosts                     Between two assets
  opsctl cp ./dump.sql.gz s3-prod:/backups/dump.sql.gz    Upload to object storage
  opsctl cp web-01:/var/log/app.log s3-prod:/logs/app.log Server to object storage
  opsctl cp -r ./dist s3-prod:/releases/v2/               Directory tree
  opsctl cp 'web-01:/var/log/*.log' s3-prod:/logs/        Remote glob (quoted)
`)
}
