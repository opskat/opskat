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
// （permission.WithPreapproved），豁免的边界因此就是这里——展开出的每一条路径都要在
// 动手之前作为具体主体被授权（spec 第 9 行的硬不变式 / D17 / D18）。
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

	dst, err := parseCpEndpoint(ctx, rest[len(rest)-1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	multiSource := recursive || len(rest) > 2
	srcs := make([]*cpEndpoint, 0, len(rest)-1)
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
		if sameOSSAsset(src, dst) {
			fmt.Fprintf(os.Stderr,
				"Note: both endpoints are on %s; the object streams through this process. "+
					"For a server-side copy use: opsctl exec %s -- \"object copy <bucket>/<key> --to=<bucket>/<key>\"\n",
				dst.asset.Name, dst.asset.Name)
		}
		multiSource = multiSource || helper.HasGlobPattern(src.path)
		srcs = append(srcs, src)
	}

	if multiSource {
		return cmdCpMultiSource(ctx, handlers, srcs, dst, recursive)
	}
	return cmdCpSingleSource(ctx, handlers, srcs[0], dst)
}

// cmdCpSingleSource 传一个被指名的文件/对象：目的地就是用户写的那个字符串，没有基点拼接，
// 因此也不要求尾随 "/"。审批排在展开之前——两端在解析时就已完全确定，先连上去问一句
// "这个路径存在吗"只会把一次不需要授权的探测塞进审批之前。
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
		return cpApprovalFailed(approvalCtx, []*cpEndpoint{src}, dst, approvalAssetID, err, result)
	}
	ctx = approvalCtx
	decision := result.ToCheckResult()
	params := cpToolParams(src.arg, dst.arg, false, src.assetID(), dst.assetID())

	// proxy 快路径复用桌面端的 SSH 连接池，因此只在远端端点全是 SSH 时启用——对象存储
	// 没有对应能力。它一次只传一个被指名的文件、且服务端不建父目录（sftp.Create），
	// 所以也只用在单源形态上，即完全等价于收敛前的四种组合。
	if proxy := cpSSHProxyClientFn(); proxy != nil && cpAllRemoteSSH(src, dst) {
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
// 两段授权：会枚举的那些源先各按自己的基点批一次"列出"（D18），再把展开出的每一条具体
// 路径塞进同一个批量对话框（D17）。该过的都过了才动第一个字节。
func cmdCpMultiSource(
	ctx context.Context, handlers map[string]tool.ToolHandlerFunc,
	srcs []*cpEndpoint, dst *cpEndpoint, recursive bool,
) int {
	// D16：目的地必须以 "/" 结尾。不复刻 POSIX cp 的目的地推断（b 存在则落 b/a），那要先
	// 探测目的地、结果依赖一次 TOCTOU 式的探测，而目的路径必须在审批之前就完全确定——
	// 批的是哪些具体路径，写的就得是哪些。
	if !strings.HasSuffix(dst.path, "/") {
		fmt.Fprintf(os.Stderr,
			"Error: destination %q must end with \"/\" when there is more than one source "+
				"(multiple arguments, --recursive, or a glob pattern): "+
				"each entry lands at <destination>/<path relative to the source base>\n", dst.raw)
		return 1
	}
	detail := cpDetail(srcs, dst)

	// 第一段·展开授权。枚举读的是元数据而不是内容，但 `cp -r web-01:/ ./x` 能把整棵文件树
	// 的结构拖出来，所以它自己要过一次授权（D18）。交给适配器的是用户指名的那个串，
	// 收窄到"实际会被枚举的基点"由适配器自己做。
	//
	// 只有真的会枚举的源要这一道：指名 N 个源却既无 -r 也无通配时没有任何结构被拖出来
	// （指名的源若是目录，无 -r 时 List 本来就直接报错），要一次"列出这个基点"的授权
	// 比"复制这一个指名对象"宽——方向上就是"批准一件事、拿到另一件"，只是宽在授权侧。
	expanded := make([]*helper.ListResult, len(srcs))
	entryCount := 0
	for i, src := range srcs {
		if cpSourceExpands(src, recursive) {
			approvalCtx, result, approvalAssetID, err := requireCpApproval(ctx,
				[]cpTarget{{ep: src, dir: helper.DirList, path: src.path}}, detail)
			ctx = approvalCtx
			if err != nil {
				return cpApprovalFailed(ctx, srcs, dst, approvalAssetID, err, result)
			}
		}

		res, err := src.adapter.List(ctx, src.asset, src.path, recursive)
		if err != nil {
			// 原样透出：List 的错误已经点名了那些目录/前缀并指向 recursive，在这里再包一层
			// 措辞只会让同一件事有两种说法。
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		if len(res.Entries) == 0 {
			// 三个适配器在零命中时一律返回空清单且不报错——那个判断留给调用方。在 cp 里它
			// 一定是错的：真实的 cp 对无匹配同样非零退出，一次静默的零文件"成功"正是 D19
			// 否决的那类"看起来成功、实际只传了一部分"。
			fmt.Fprintf(os.Stderr, "Error: no files matched %q\n", src.raw)
			return 1
		}
		expanded[i] = res
		entryCount += len(res.Entries)
	}
	if entryCount > cpMaxEntries {
		// 上限不是性能考虑，是审批质量：五千条的对话框不是决策，是橡皮图章。截断到前 200 条
		// 会换来更糟的东西——一次看起来成功、实际只复制了一部分的传输（D19）。
		fmt.Fprintf(os.Stderr,
			"Error: the sources expand to %d entries, more than the %d that can be reviewed in one approval; "+
				"narrow the pattern (nothing was transferred)\n", entryCount, cpMaxEntries)
		return 1
	}

	// 第二段·传输授权。为每个 entry 生成源读 + 目的写两个主体，读写成对相邻：一条待批准的
	// 传输是"从这里读、写到那里"，把两半拆到一份清单的首尾两段，用户就没法逐条看出哪个
	// 文件会落到哪里。
	subjects := make([]cpSubject, 0, 2*entryCount)
	seen := make(map[cpSubject]bool, 2*entryCount)
	plans := make([]cpTransferPlan, 0, len(srcs))
	for i, src := range srcs {
		for _, entry := range expanded[i].Entries {
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
		plans = append(plans, cpTransferPlanFor(src, dst, expanded[i], recursive))
	}

	batchResult, err := cpBatchApprovalFn(ctx, subjects, detail)
	if batchResult.SessionID != "" {
		ctx = aictx.WithSessionID(ctx, batchResult.SessionID)
	}
	if err != nil {
		return cpApprovalFailed(ctx, srcs, dst, 0, err, batchResult)
	}
	decision := batchResult.ToCheckResult()

	// 快速失败：任一条源出错立即中止（D19）。不采用 POSIX cp 的"继续并最终非零"——每个已
	// 传输的字节都是一次已批准的副作用，出意外后继续会留下一个看起来完整、实际残缺的
	// 目的地，而"残缺"这件事从目的端是看不出来的。
	for i, plan := range plans {
		params := cpToolParams(plan.src.arg, plan.dstArg, recursive, plan.src.assetID(), dst.assetID())
		if plan.listing != nil {
			params[tool.CpApprovedListingArg] = plan.listing
		}
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
	// listing 是这条源刚刚被逐条审批过的那份展开结果，枚举形态下原样交给工具，工具据此
	// 传输而**不再自己展开**（tool.CpApprovedListingArg）。单源形态为 nil：那条路的两端都是
	// 字面量，没有第二次展开，也就没有这条缝。
	listing *helper.ListResult
}

// cpTransferPlanFor 按源的形态定这条源怎么交给工具。落点与清单必须由同一处决定：拼好的
// 具体落点配上一份按 RelPath 算落点的清单，是两种落点语义打架。
//
// 被指名的源恒展开成一条（指名一个目录/前缀却没有 recursive 时 List 已经报错了，见
// TransferAdapter.List），拼出来的落点与它自己那条审批主体逐字相同。
func cpTransferPlanFor(src, dst *cpEndpoint, listing *helper.ListResult, recursive bool) cpTransferPlan {
	if cpSourceExpands(src, recursive) {
		return cpTransferPlan{src: src, dstArg: dst.arg, listing: listing}
	}
	return cpTransferPlan{src: src, dstArg: dst.argFor(dst.path + listing.Entries[0].RelPath)}
}

// cpSourceExpands 报告这一条源是不是**枚举**形态：recursive 为真，或路径含 glob 元字符。
// 判的是形态不是命中条数——一个只命中一个文件的 glob 仍然是枚举形态。与 AI 侧 handleCp 的
// 多源形态判定同一条（spec §6.5「何时算多源」），两条入口因此在同一件事上给同一个答案。
func cpSourceExpands(src *cpEndpoint, recursive bool) bool {
	return recursive || helper.HasGlobPattern(src.path)
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

// sameOSSAsset 报告两端是不是同一个对象存储资产（D12）。
func sameOSSAsset(src, dst *cpEndpoint) bool {
	return src.isRemote() && dst.isRemote() &&
		src.asset.ID == dst.asset.ID && src.asset.Type == asset_entity.AssetTypeOSS
}

// cpAllRemoteSSH 报告这些端点里的远端端点是不是全是 SSH。本地端点不参与——它没有资产，
// 也就没有协议。
func cpAllRemoteSSH(eps ...*cpEndpoint) bool {
	for _, ep := range eps {
		if ep.isRemote() && !ep.asset.IsSSH() {
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
		approvalType, subject := tg.ep.adapter.ApprovalSubject(tg.path, tg.dir)
		result, err := cpApprovalFn(ctx, approval.ApprovalRequest{
			Type:      permission.ApprovalTypeFor(approvalType),
			AssetID:   tg.ep.asset.ID,
			AssetName: tg.ep.asset.Name,
			Command:   subject,
			Detail:    detail,
			SessionID: aictx.GetSessionID(ctx),
		})
		last = result
		lastAssetID = tg.ep.asset.ID
		// 审批会在没有 session 时自动建一个，后续那次审批与审计都要归到同一个 session
		if result.SessionID != "" {
			ctx = aictx.WithSessionID(ctx, result.SessionID)
		}
		if err != nil {
			return ctx, result, tg.ep.asset.ID, err
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
	approvalType, command := ep.adapter.ApprovalSubject(path, dir)
	subject := cpSubject{
		approvalType: approvalType,
		assetID:      ep.asset.ID,
		assetName:    ep.asset.Name,
		command:      command,
	}
	if seen[subject] {
		return subjects
	}
	seen[subject] = true
	return append(subjects, subject)
}

// cpMaxEntries 是一次 cp 允许展开出的条目上限（D19）。超出即报错，不截断。
const cpMaxEntries = 200

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

// requireCpBatchApproval 让展开出的每一条主体过一次授权：逐条查策略与 grant，需要确认的
// **全部塞进同一个审批对话框**（spec §6.5 第二段 / D17），与 AI 侧 checkAccessBatch 同形。
// 逐条弹 N 次框不可用；批一条递归 pattern 会顺带放宽 local_write/local_edit 那道共用的
// 匹配器，因此主体始终是展开后的具体路径。
//
// detail 是这次传输的"从哪到哪"（cpDetail），原样搭在每一条 item 上——折叠摘要（§8 item 7）
// 靠它显示"两端基点"，单端点那条路（requireCpApproval）早已这么做。
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
			items = append(items, approval.BatchItem{
				Type:      permission.ApprovalTypeFor(subject.approvalType),
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
	ctx context.Context, srcs []*cpEndpoint, dst *cpEndpoint, approvalAssetID int64,
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
	params := cpToolParams(strings.Join(srcArgs, " "), dst.arg, false, srcAssetID, dst.assetID())
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
	fmt.Fprintf(os.Stderr, "Error: %v\n", approvalErr)
	return 1
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
  opsctl [--session <id>] cp [-r] <source>... <destination>

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
  The sources may expand to at most 200 entries: beyond that the whole transfer
  is an error, never a partial copy. Symlinks are skipped and reported.

Approval:
  Every asset endpoint is authorized separately under that asset's own policy,
  before any byte is transferred. Recursive/glob transfers first ask for
  permission to list the source, then batch every expanded path into a single
  approval dialog.

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
