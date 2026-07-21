package command

import (
	"context"
	"fmt"
	"os"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/sshpool"
)

func cmdCp(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printCpUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if len(args) < 2 {
		printCpUsage()
		return 1
	}

	src, dst := args[0], args[1]
	srcAssetID, srcPath, err := parseRemotePathCtx(ctx, src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	dstAssetID, dstPath, err := parseRemotePathCtx(ctx, dst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	srcIsRemote := srcAssetID > 0
	dstIsRemote := dstAssetID > 0

	argsJSON := fmt.Sprintf(`{"src":%q,"dst":%q}`, src, dst)

	if session != "" {
		ctx = aictx.WithSessionID(ctx, session)
	}

	// 文件传输与执行命令等价（写 authorized_keys / cron / 被 systemd 引用的脚本都够
	// 换来一次执行），因此必须与 exec 一样过审批。审批放在 proxy 与直连分支的共同上游，
	// 否则走 proxy 的那条路径会漏。
	approvalCtx, approvalResult, err := requireCpApproval(ctx, srcAssetID, srcPath, dstAssetID, dstPath, src, dst)
	if err != nil {
		writeOpsctlAudit(approvalCtx, "cp", argsJSON, "", err, approvalResult.ToCheckResult())
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	ctx = approvalCtx
	decision := approvalResult.ToCheckResult()

	// 尝试通过 proxy 执行文件传输
	if proxy := getSSHProxyClient(); proxy != nil {
		exitCode := cmdCpViaProxy(proxy, srcAssetID, srcPath, dstAssetID, dstPath, srcIsRemote, dstIsRemote, src, dst)
		var cpErr error
		if exitCode != 0 {
			cpErr = fmt.Errorf("cp via proxy failed with exit code %d", exitCode)
		}
		writeOpsctlAudit(ctx, "cp", argsJSON, fmt.Sprintf(`{"status":"completed","exit_code":%d}`, exitCode), cpErr, decision)
		return exitCode
	}

	// Fallback: 直连
	var cpErr error
	switch {
	case !srcIsRemote && !dstIsRemote:
		fmt.Fprintln(os.Stderr, "Error: at least one path must be remote (<asset>:<path>)")
		return 1

	case !srcIsRemote && dstIsRemote:
		// Upload: local -> remote
		exitCode := callHandler(ctx, handlers, "upload_file", map[string]any{
			"asset_id":    float64(dstAssetID),
			"local_path":  src,
			"remote_path": dstPath,
		}, decision)
		return exitCode

	case srcIsRemote && !dstIsRemote:
		// Download: remote -> local
		exitCode := callHandler(ctx, handlers, "download_file", map[string]any{
			"asset_id":    float64(srcAssetID),
			"remote_path": srcPath,
			"local_path":  dst,
		}, decision)
		return exitCode

	default:
		// Asset-to-asset transfer: remote -> remote
		cpErr = helper.CopyBetweenAssets(ctx, srcAssetID, srcPath, dstAssetID, dstPath)
		auditResult := `{"status":"completed"}`
		if cpErr != nil {
			auditResult = fmt.Sprintf(`{"error":%q}`, cpErr.Error())
		}
		writeOpsctlAudit(ctx, "cp", argsJSON, auditResult, cpErr, decision)
		if cpErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", cpErr)
			return 1
		}
		return 0
	}
}

// cpApprovalFn 是 cp 的审批入口。用变量而非直接调用是为了可测——与本包的
// opsctlAuditWriter 同一套路：测试替换掉它，避免真的去连桌面端审批 socket。
var cpApprovalFn = requireApproval

// requireCpApproval 为一次传输发起审批，审批主体是**远端路径**（grant 按资产存，
// 本地路径不属于任何资产，塞进 pattern 无法匹配）。
//
// 资产间传输（remote → remote）要审两次：先源端读，再目的端写。任一被拒即整体失败。
// 返回的 ctx 带上审批分配的 sessionID，供审计归组。
func requireCpApproval(ctx context.Context, srcAssetID int64, srcPath string, dstAssetID int64, dstPath, src, dst string) (context.Context, ApprovalResult, error) {
	detail := fmt.Sprintf("opsctl cp %s %s", src, dst)

	type target struct {
		assetID int64
		path    string
	}
	var targets []target
	if srcAssetID > 0 {
		targets = append(targets, target{srcAssetID, srcPath})
	}
	if dstAssetID > 0 {
		targets = append(targets, target{dstAssetID, dstPath})
	}

	var last ApprovalResult
	for _, tg := range targets {
		result, err := cpApprovalFn(ctx, approval.ApprovalRequest{
			Type:      "cp",
			AssetID:   tg.assetID,
			AssetName: assetNameForApproval(ctx, tg.assetID),
			Command:   tg.path,
			Detail:    detail,
			SessionID: aictx.GetSessionID(ctx),
		})
		last = result
		// 审批会在没有 session 时自动建一个，后续那次审批与审计都要归到同一个 session
		if result.SessionID != "" {
			ctx = aictx.WithSessionID(ctx, result.SessionID)
		}
		if err != nil {
			return ctx, result, err
		}
	}
	return ctx, last, nil
}

// assetNameForApproval 取资产名用于审批弹窗展示。资产在 parseRemotePathCtx 里已经解析过
// 一次，这里再取不到只影响展示，不阻断流程——与 permission.HandleConfirm 的处理一致。
func assetNameForApproval(ctx context.Context, assetID int64) string {
	asset, err := asset_repo.Asset().Find(ctx, assetID)
	if err != nil {
		logger.Ctx(ctx).Warn("get asset for cp approval", zap.Int64("assetID", assetID), zap.Error(err))
		return ""
	}
	return asset.Name
}

// cmdCpViaProxy 通过 proxy 执行文件传输
func cmdCpViaProxy(proxy *sshpool.Client, srcAssetID int64, srcPath string, dstAssetID int64, dstPath string, srcIsRemote, dstIsRemote bool, src, dst string) int {
	switch {
	case !srcIsRemote && !dstIsRemote:
		fmt.Fprintln(os.Stderr, "Error: at least one path must be remote (<asset>:<path>)")
		return 1

	case !srcIsRemote && dstIsRemote:
		// Upload: local -> remote
		f, err := os.Open(src) //nolint:gosec // src is a user-provided local file path
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		if err := proxy.Upload(sshpool.ProxyRequest{
			AssetID: dstAssetID,
			DstPath: dstPath,
		}, f); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case srcIsRemote && !dstIsRemote:
		// Download: remote -> local
		f, err := os.Create(dst) //nolint:gosec // dst is a user-provided local file path
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		if err := proxy.Download(sshpool.ProxyRequest{
			AssetID: srcAssetID,
			SrcPath: srcPath,
		}, f); err != nil {
			_ = os.Remove(dst) //nolint:gosec // dst is a user-provided local file path
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	default:
		// Asset-to-asset transfer: remote -> remote
		if err := proxy.Copy(sshpool.ProxyRequest{
			AssetID:    dstAssetID,
			SrcAssetID: srcAssetID,
			SrcPath:    srcPath,
			DstPath:    dstPath,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}
}

func printCpUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] cp <source> <destination>

Path Format:
  Local path:   /path/to/file  or  ./relative/path
  Remote path:  <asset>:<remote-path>  (asset name or ID)

At least one of source or destination must be a remote path.

Transfer Modes:
  Local -> Remote     Upload a file to a remote server via SFTP
  Remote -> Local     Download a file from a remote server via SFTP
  Remote -> Remote    Stream a file directly between two assets (no local disk)

Examples:
  opsctl cp ./config.yml web-server:/etc/app/config.yml   Upload by name
  opsctl cp 1:/var/log/app.log ./app.log                  Download by ID
  opsctl cp 1:/etc/hosts 2:/tmp/hosts                     Transfer between assets
`)
}
