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

// cpEndpoint 是解析后的 cp 的一端：资产（本地端为 nil）、该端点上的路径，以及它的适配器。
type cpEndpoint struct {
	raw     string
	asset   *asset_entity.Asset
	path    string
	adapter helper.TransferAdapter
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
//     被拒即整体失败，**且在任何字节被读写之前**。
//  6. 传输：OpenRead → Write。
//
// 单源形态下审批排在展开（List）之前：被指名的那个路径两端都已知，不必先连上去问一句
// 存在不存在——那正是收敛前 checkFileTransfer 守着的不变式（"在真正建连之前过一次
// cp 审批"）。多源形态没有这个选择，展开必须先发生，所以它按 D18 先过一次 DirList 授权。
//
// **本地端点不产生审批项**（spec §6.2），本轮沿用这条既有裁定，不是默认继承：
// download_file 今天就能写到本地任意路径，唯一的门是远端那一端的读授权。理由是本地写的
// 位置完全由被批准的那条命令串决定——D11 强制绝对路径，端点原文又原样出现在审批项的
// Detail 与 audit_logs 里，用户批准"读 s3-prod:/b/k → 写 /etc/passwd"时看到的就是它。
// 已知代价：cp 的本地写不经过 local_write / local_edit 那套会话内路径白名单
// （local_tool_gate.go），因此一个远端源 + cp 可以绕开那份白名单落到本地任意位置。
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
	return cpSummary(res.Entries[0].Size, res.SkippedSymlinks)
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
	// 整棵文件树的结构拖出来，所以它在 List 之前先过一次 DirList 授权，主体是展开基点。
	base := helper.ExpansionBase(src.path)
	if err := checkEndpoint(ctx, checker, src, helper.DirList, base, detail); err != nil {
		return "", err
	}

	res, err := expandSource(ctx, src, src.path, recursive)
	if err != nil {
		return "", err
	}
	if len(res.Entries) > 1 {
		return "", fmt.Errorf(
			"%q expands to %d entries; cp transfers one entry per call for now — narrow the source pattern",
			src.raw, len(res.Entries))
	}

	// 目的路径 = 目的基点 + RelPath。形态校验只能排在展开之后（这条路径此刻才存在），
	// 但仍然排在这条传输的两个审批项之前——展开授权是另一件事，它批的是"列出源端"。
	entry := res.Entries[0]
	dstPath := dst.path + entry.RelPath
	if err := dst.adapter.ValidateDestination(dstPath); err != nil {
		return "", err
	}
	if err := checkEndpoint(ctx, checker, src, helper.DirRead, entry.Path, detail); err != nil {
		return "", err
	}
	if err := checkEndpoint(ctx, checker, dst, helper.DirWrite, dstPath, detail); err != nil {
		return "", err
	}
	if err := transferOne(ctx, src, dst, entry, dstPath); err != nil {
		return "", err
	}
	return cpSummary(entry.Size, res.SkippedSymlinks)
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

func cpSummary(bytes int64, skipped []string) (string, error) {
	out, err := json.Marshal(cpResult{Transferred: 1, Bytes: bytes, Skipped: skipped})
	if err != nil {
		return "", fmt.Errorf("marshal cp result: %w", err)
	}
	return string(out), nil
}
