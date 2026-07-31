package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// cpMaxEntries 是一次 cp 允许展开出的条目上限（D19）。超出即报错，不截断。
//
// 上限不是性能考虑，是审批质量：五千条的对话框不是决策，是橡皮图章。而截断到前 200 条
// 会换来更糟的东西——一次看起来成功、实际只复制了一部分的传输。
const cpMaxEntries = 200

// cpEndpoint 是解析后的 cp 的一端：资产（本地端为 nil）、该端点上的路径，以及它的适配器。
type cpEndpoint struct {
	raw     string
	asset   *asset_entity.Asset
	path    string
	adapter helper.TransferAdapter
}

// cpAccess 是一条待授权的端点访问：某个端点上的某条**具体**路径，在某个方向上。
// 展开出的每条路径各产生一条源读、一条目的写，它们一起进同一次批量审批（D17）。
type cpAccess struct {
	ep   *cpEndpoint
	path string
	dir  helper.Direction
}

// isRemote 报告这一端有没有资产。**有资产才有审批主体**——本地端点没有资产，正确的调用方
// 压根不会去问它要主体（见 helper.TransferAdapter.ApprovalSubject 的文档注释）。
func (e *cpEndpoint) isRemote() bool { return e.asset != nil }

// cpResult 是 cp 返回给调用方的汇总（spec §6.3）。逐条回流会淹掉模型的上下文，但跳过的
// 符号链接必须报出来——静默跳过就是一次看起来完整、实际残缺的传输。
type cpResult struct {
	Transferred int      `json:"transferred"`
	Bytes       int64    `json:"bytes"`
	Skipped     []string `json:"skipped,omitempty"`
}

// handleCp 是传输面的唯一入口，取代了 upload_file / download_file 两个工具。两端各自是
// 本地、SSH 或对象存储，九种组合塌缩成一条"展开 → 逐端点审批 → OpenRead → Write"，
// 与 opsctl cp 共用同一套端点适配器（spec §6.2 / D3 / D14）。
//
// 执行顺序与 handleExec 同源，不能重排：无副作用的判断必须全部走完，才能碰有副作用的
// 那一步（审批弹窗会阻塞等待用户）。
//
//  1. 参数与端点解析（helper.ParseTransferEndpoint）——语法错、资产查不到都在这里返回。
//  2. 至少一端必须是资产：本地↔本地不属于传输面（现状不变）。
//  3. 本地路径必须绝对（D11）。
//  4. 目的端形态校验（ValidateDestination）**排在任何审批项之前**：目的端不经过 List、
//     ApprovalSubject 又不能报错，少了这一关，一个形态错误的目的地会带着错误的主体走完
//     审批、到 Write 才失败——用户批准了一件必然失败的事，还可能顺手落下一条比这次操作
//     更宽的常驻授权。
//  5. 逐端点审批：每个远端端点向自己的适配器索取 ApprovalSubject，各自过一次权限检查
//     （SSH 是 cp + 远端路径，OSS 是 oss + `object.read/write <bucket>/<key>`）。任一端
//     被拒即整体失败，**且在任何字节被读写之前**。多源形态下"每个端点"是**展开后的每条
//     具体路径**，它们一起进同一次批量审批（D17）。
//  6. 传输：OpenRead → Write。
//
// 单源形态下审批排在展开（List）之前：被指名的那个路径两端都已知，不必先连上去问一句
// 存在不存在——那正是收敛前 checkFileTransfer 守着的不变式（"在真正建连之前过一次
// cp 审批"）。多源形态没有这个选择，展开必须先发生，所以它按 D18 先过一次 DirList 授权。
//
// **本地端点不产生审批项**（spec §6.2），本轮沿用这条既有裁定，不是默认继承：
// download_file 今天就能写到本地任意路径，唯一的门是远端那一端的读授权；本地路径只以
// 展示的身份出场——D11 强制它绝对，端点原文又原样进审批项的 Detail 与 audit_logs。
// 两条代价要说准，它们都比"本地路径在用户批准的那条串里看得见"这句话大：
//
//  1. 远端那一端判成 Allow 时**根本没有弹窗**，于是也没有"用户批准的那条串"。OSS 资产
//     的内置默认策略就放行 object.read *（builtin:oss-readonly），所以在一个刚建好、
//     谁也没改过策略的对象存储资产上，`cp s3:/b/k /Users/me/.ssh/authorized_keys`
//     零交互跑完，事后只在 audit_logs 里留一行。SSH 端要一条既有 grant 才走到这一步，
//     对象存储端不需要——这是本轮把 OSS 接进传输面新带来的一档。
//  2. 本地写因此也不经过 local_write / local_edit 那道门（local_tool_gate.go）。那不只是
//     一份"会话内白名单"：未命中白名单的路径要弹一次框，没接审批回调时直接拒。
//
// 改掉它属于本地工具授权面的重做（那是一条 cago middleware，不是可调用的检查），
// 与 OSS 无关，不在本轮范围内——记在这里是为了它是一条被裁定过的选择，而不是没人看见。
func handleCp(ctx context.Context, args map[string]any) (string, error) {
	srcRaw := aictx.ArgString(args, "src")
	dstRaw := aictx.ArgString(args, "dst")
	if srcRaw == "" || dstRaw == "" {
		return "", fmt.Errorf("missing required parameters: src, dst")
	}
	recursive := aictx.ArgBool(args, "recursive")

	src, err := parseCpEndpoint(ctx, srcRaw)
	if err != nil {
		return "", err
	}
	dst, err := parseCpEndpoint(ctx, dstRaw)
	if err != nil {
		return "", err
	}
	if !src.isRemote() && !dst.isRemote() {
		return "", fmt.Errorf(
			"at least one endpoint must be on an asset (<asset>:/<path>); %q → %q copies nothing remote",
			srcRaw, dstRaw)
	}

	// detail 只影响审批项的展示，不参与任何匹配——但它是用户唯一能看见"这次传输的另一端
	// 是哪里"的地方（本地端没有自己的审批项），所以两端的原文都在里面。
	detail := fmt.Sprintf("cp %s → %s", srcRaw, dstRaw)

	// checker 为 nil 只在 opsctl 那条已预检的路径上合法（permission.WithPreapproved），
	// 其余情况直接报错——漏接线不能等于放行。
	checker, err := permission.RequireCheckerOrPreapproved(ctx)
	if err != nil {
		return "", err
	}

	if recursive || helper.HasGlobPattern(src.path) {
		return cpMultiSource(ctx, checker, src, dst, recursive, detail)
	}
	return cpSingleSource(ctx, checker, src, dst, detail)
}

// parseCpEndpoint 把一个端点串解析成 (资产, 路径, 适配器)。语法由 helper.ParseTransferEndpoint
// 定义、两个入口共用，ref 的解析由入口注入：AI 侧是 assetref.Resolve（数字 id 或精确名称）。
func parseCpEndpoint(ctx context.Context, raw string) (*cpEndpoint, error) {
	asset, p, err := helper.ParseTransferEndpoint(ctx, raw, assetref.Resolve)
	if err != nil {
		return nil, err
	}
	adapter, err := helper.TransferAdapterFor(asset)
	if err != nil {
		return nil, err
	}
	// D11：AI 侧的本地路径必须绝对。审批项与 audit_logs.command 记的就是这条端点串，而
	// 相对路径要靠一个串里看不见的工作目录才能定位——批准的字符串因此指不到一个确定的
	// 文件。opsctl 的相对路径手感不受影响：那条入口在调用这里之前就展开成绝对路径了。
	if asset == nil && !filepath.IsAbs(p) {
		return nil, fmt.Errorf(
			"local path %q must be absolute: it is resolved by this process, whose working directory is not "+
				"part of the approved command string", raw)
	}
	return &cpEndpoint{raw: raw, asset: asset, path: p, adapter: adapter}, nil
}

// cpSingleSource 传一个被指名的文件/对象：目的地就是用户写的那个字符串，没有基点拼接，
// 因此也不要求尾随 "/"。
func cpSingleSource(
	ctx context.Context, checker *permission.CommandPolicyChecker, src, dst *cpEndpoint, detail string,
) (string, error) {
	if err := dst.adapter.ValidateDestination(dst.path); err != nil {
		return "", err
	}
	if err := checkEndpoint(ctx, checker, src, helper.DirRead, src.path, detail); err != nil {
		return "", err
	}
	if err := checkEndpoint(ctx, checker, dst, helper.DirWrite, dst.path, detail); err != nil {
		return "", err
	}

	// 展开排在审批之后：单源的两端在解析时就已完全确定，先连上去 Stat 一次只会让"这个
	// 路径存在吗"变成一次不需要授权就能问的问题。List 而不是直接 OpenRead，是为了拿到
	// 大小（OSS 的 PutObject 用它决定要不要分片），以及"指名了一个目录却没有 recursive"
	// 这类它已经说得比 OpenRead 清楚的错误。
	res, err := expandSource(ctx, src, src.path, false)
	if err != nil {
		return "", err
	}
	if err := transferOne(ctx, src, dst, res.Entries[0], dst.path); err != nil {
		return "", err
	}
	return cpSummary(1, res.Entries[0].Size, res.SkippedSymlinks)
}

// cpMultiSource 处理多源形态：recursive 为真，或源路径含 glob 元字符（spec §6.5）。
// 判的是**形态**不是命中条数——一个只命中一个文件的 glob 仍然是多源，它的落点是
// 目的基点 + RelPath 而不是字面 dst。
func cpMultiSource(
	ctx context.Context, checker *permission.CommandPolicyChecker,
	src, dst *cpEndpoint, recursive bool, detail string,
) (string, error) {
	// D16：目的地必须以 "/" 结尾。不复刻 POSIX cp 的目的地推断（b 存在则落 b/a），那要先
	// 探测目的地、结果依赖一次 TOCTOU 式的探测，而目的路径必须在审批之前就完全确定——
	// 批的是哪些具体路径，写的就得是哪些。
	if !strings.HasSuffix(dst.path, "/") {
		return "", fmt.Errorf(
			"destination %q must end with \"/\" when the source expands to multiple entries "+
				"(recursive or a glob pattern): each entry lands at <destination>/<path relative to the source base>",
			dst.raw)
	}

	// D18：展开自己是一种能力。枚举读的是元数据而不是内容，但 `cp -r web-01:/ ./x` 能把
	// 整棵文件树的结构拖出来，所以它在 List 之前先过一次 DirList 授权。
	//
	// 交给适配器的是用户指名的那个串，与 List 收到的完全相同：收窄到"实际会被枚举的基点"
	// 由适配器自己做（见 helper.TransferAdapter.ApprovalSubject）。这里替它按 glob 截一刀，
	// 对象存储那一端就会把前缀里的字面量 "[" 当成通配、把基点塌到桶根上——指名一个前缀，
	// 换来一条整桶列举的常驻授权。
	if err := checkEndpoint(ctx, checker, src, helper.DirList, src.path, detail); err != nil {
		return "", err
	}

	res, err := expandSource(ctx, src, src.path, recursive)
	if err != nil {
		return "", err
	}
	if len(res.Entries) > cpMaxEntries {
		return "", fmt.Errorf(
			"%q expands to %d entries, more than the %d that can be reviewed in one approval; "+
				"narrow the source pattern (nothing was transferred)",
			src.raw, len(res.Entries), cpMaxEntries)
	}

	// 目的路径 = 目的基点 + RelPath。形态校验只能排在展开之后（这些路径此刻才存在），
	// 但仍然排在这次传输的审批项之前——展开授权是另一件事，它批的是"列出源端"。
	dstPaths := make([]string, len(res.Entries))
	accesses := make([]cpAccess, 0, 2*len(res.Entries))
	for i, entry := range res.Entries {
		if !relPathStaysUnderBase(entry.RelPath) {
			return "", fmt.Errorf(
				"%q expands to an entry whose path relative to the source base is %q, which does not stay under "+
					"the destination %q; refusing the whole transfer (nothing was transferred)",
				src.raw, entry.RelPath, dst.raw)
		}
		dstPath := dst.path + entry.RelPath
		if err := dst.adapter.ValidateDestination(dstPath); err != nil {
			return "", err
		}
		dstPaths[i] = dstPath
		// 读与写成对相邻：一条待批准的传输是"从这里读、写到那里"，把两半拆到一份
		// 两百行清单的首尾两段，用户就没法逐条看出哪个文件会落到哪里。
		accesses = append(accesses,
			cpAccess{ep: src, path: entry.Path, dir: helper.DirRead},
			cpAccess{ep: dst, path: dstPath, dir: helper.DirWrite})
	}
	if err := checkAccessBatch(ctx, checker, accesses, detail); err != nil {
		return "", err
	}

	// 快速失败：任一条出错立即中止（D19）。不采用 POSIX cp 的"继续并最终非零"——每个已
	// 传输的字节都是一次已批准的副作用，出意外后继续会留下一个看起来完整、实际残缺的
	// 目的地。已经落地的那 N 条要在错误里报出来，而不是假装这次传输什么都没做。
	var totalBytes int64
	for i, entry := range res.Entries {
		if err := transferOne(ctx, src, dst, entry, dstPaths[i]); err != nil {
			return "", fmt.Errorf("transferred %d/%d entries, then %q → %q failed: %w",
				i, len(res.Entries), entry.Path, dstPaths[i], err)
		}
		totalBytes += entry.Size
	}
	return cpSummary(len(res.Entries), totalBytes, res.SkippedSymlinks)
}

// relPathStaysUnderBase 报告一条展开出来的 RelPath 拼到目的基点上之后，还落不落在这个
// 基点之下。
//
// 三个展开来源里只有对象存储端产得出走出去的落点：本地与 SFTP 的 RelPath 由目录项拼成，
// 单个名字里不可能有分隔符；而对象 key 是一段不透明的字节串，`logs/../../id_rsa` 是一个
// 合法的 S3 key，相对 `logs/` 前缀展开出来的落点就是 `../../id_rsa`。
// 目的端在本地时没有任何东西挡它——localAdapter.ValidateDestination 明写
// 本地文件系统对写入目标没有形态约束，Write 又会按需建父目录——于是这一条落到用户指名的
// 目的地之外，汇总还照样报 transferred。批的是清单上那些路径，写的就必须是那些（D16/D17）。
//
// 判据取 filepath.IsLocal：它按运行平台的词法判断"这条相对路径会不会走出它所在的子树"。
// 选它而不是自己按 "/" 切段找 ".."，是因为 RelPath 的 "/" 分隔契约只约束得了展开侧——
// 对象 key 里的 "\" 是字面量，落到 Windows 本地端却是分隔符，`..\..\x` 在那里同样逃得出去。
func relPathStaysUnderBase(relPath string) bool {
	return filepath.IsLocal(relPath)
}

// checkAccessBatch 让一批端点访问过一次授权：每条各自查策略，**需要确认的全部塞进同一个
// 审批对话框**（spec §6.5 第二段 / D17）。逐条弹 N 次框不可用，批一条递归 pattern 则会
// 顺带放宽 local_write/local_edit 那道共用的匹配器——主体始终是展开后的具体路径。
//
// "有资产才有审批主体"这条判断只出现在下面那一行：其余部分（主体去重、策略检查、批量
// 对话框、拒绝处理）对端点一视同仁，本地端点将来若要产生审批项，改的是那一行而不是这条流程。
func checkAccessBatch(
	ctx context.Context, checker *permission.CommandPolicyChecker, accesses []cpAccess, detail string,
) error {
	// checker 为 nil 只在 opsctl 那条已预检的路径上合法（上游 RequireCheckerOrPreapproved
	// 已经挡掉漏接线），与 checkEndpoint 同一条豁免。
	if checker == nil {
		return nil
	}

	items := make([]permission.ApprovalItem, 0, len(accesses))
	seen := make(map[permission.ApprovalItem]bool, len(accesses))
	for _, access := range accesses {
		if !access.ep.isRemote() {
			continue
		}
		approvalType, subject := access.ep.adapter.ApprovalSubject(access.path, access.dir)
		item := permission.ApprovalItem{
			Type:      permission.ApprovalTypeFor(approvalType),
			AssetID:   access.ep.asset.ID,
			AssetName: access.ep.asset.Name,
			Command:   subject,
			Detail:    detail,
		}
		// 同一条主体只查一次、只出现一次：源与目的落在同一个前缀上时读写主体逐字相同，
		// 重复条目让用户在同一份清单里读两遍同一句话，查一遍策略也是白查。
		if seen[item] {
			continue
		}
		seen[item] = true

		result := permission.CheckPermission(ctx, approvalType, access.ep.asset.ID, subject)
		aictx.RecordDecision(ctx, result)
		switch result.Decision {
		case aictx.Deny:
			return fmt.Errorf("%s", result.Message)
		case aictx.NeedConfirm:
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil
	}

	confirm := checker.ConfirmFunc()
	if confirm == nil {
		return fmt.Errorf("transfer requires confirmation but no approval mechanism is configured")
	}
	resp := confirm(ctx, permission.ApprovalKindBatch, items)
	parsed, parseErr := permission.ParseApprovalResponse(permission.ApprovalKindBatch, resp, items)
	if parseErr != nil || parsed.Decision != permission.ApprovalAllow {
		// 响应解析失败与用户点拒绝合成同一条出路，与 HandleConfirm 同一裁定：两者都不是
		// 授权，而模型该做的事（立刻停下）也是同一件。
		msg := fmt.Sprintf(
			"USER DENIED: The user has denied this transfer (%d paths). Stop the current task immediately.",
			len(items))
		aictx.RecordDecision(ctx, aictx.CheckResult{
			Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny, Message: msg,
		})
		return fmt.Errorf("%s", msg)
	}
	aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow})
	return nil
}

// expandSource 展开源端，并把"什么都没匹配上"翻译成错误。
//
// 三个适配器在零命中时一律返回空 ListResult 且不报错：适配器判断不了这次零命中是不是
// 用户想要的，所以那个判断留给调用方（任务 7 的定案）。而在 cp 里它一定是错的——真实的
// cp 对无匹配同样非零退出，一次静默的零文件"成功"正是 D19 否决的那类"看起来成功、实际
// 只传了一部分"。Go 的 Glob 不像 shell 那样展开 `*/`，`cp 'web-01:/var/log/*/' ./x`
// 这个形态尤其容易踩。
func expandSource(
	ctx context.Context, src *cpEndpoint, pattern string, recursive bool,
) (*helper.ListResult, error) {
	res, err := src.adapter.List(ctx, src.asset, pattern, recursive)
	if err != nil {
		// 原样透出：List 的错误已经点名了那些目录/前缀并指向 recursive，在这里再包一层
		// 措辞只会让同一件事有两种说法。
		return nil, err
	}
	if len(res.Entries) == 0 {
		return nil, fmt.Errorf("no files matched %q", pattern)
	}
	return res, nil
}

// checkEndpoint 让一个端点在某个方向上过一次权限检查。
//
// **有资产才有审批主体**：本地端点没有资产，这里直接放行——不是靠"审批类型是空串"来判断，
// 那种写法在有人忘记检查时是 fail-open（helper.TransferAdapter.ApprovalSubject 的注释）。
//
// checker 为 nil 只在 opsctl 已完成审批并 WithPreapproved 标记过时合法，其余缺 checker 的
// 路径由上游的 RequireCheckerOrPreapproved 挡掉。
func checkEndpoint(
	ctx context.Context, checker *permission.CommandPolicyChecker,
	ep *cpEndpoint, dir helper.Direction, path, detail string,
) error {
	if !ep.isRemote() || checker == nil {
		return nil
	}
	approvalType, subject := ep.adapter.ApprovalSubject(path, dir)
	result := checker.CheckForAsset(ctx, ep.asset.ID, approvalType, subject, detail)
	aictx.RecordDecision(ctx, result)
	if result.Decision != aictx.Allow {
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

// transferOne 执行一条已获批准的传输：源端开读、目的端流式写入。
func transferOne(ctx context.Context, src, dst *cpEndpoint, entry helper.Entry, dstPath string) error {
	logger.Ctx(ctx).Info("cp transfer start",
		zap.String("src", entry.Path), zap.String("dst", dstPath), zap.Int64("size", entry.Size))

	rc, size, err := src.adapter.OpenRead(ctx, src.asset, entry.Path)
	if err != nil {
		logger.Ctx(ctx).Error("cp open source", zap.String("src", entry.Path), zap.Error(err))
		return err
	}
	defer func() {
		if err := rc.Close(); err != nil && !helper.IsExpectedCloseErr(err) {
			logger.Ctx(ctx).Warn("close transfer source", zap.String("src", entry.Path), zap.Error(err))
		}
	}()

	if err := dst.adapter.Write(ctx, dst.asset, dstPath, rc, size); err != nil {
		logger.Ctx(ctx).Error("cp write destination", zap.String("dst", dstPath), zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("cp transfer done", zap.String("src", entry.Path), zap.String("dst", dstPath))
	return nil
}

func cpSummary(transferred int, bytes int64, skipped []string) (string, error) {
	out, err := json.Marshal(cpResult{Transferred: transferred, Bytes: bytes, Skipped: skipped})
	if err != nil {
		return "", fmt.Errorf("marshal cp result: %w", err)
	}
	return string(out), nil
}
