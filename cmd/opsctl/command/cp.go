package command

import (
	"context"
	"fmt"
	"os"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/tool"
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

	// cp 不需要审批；若调用方提供了 session，仍注入到 ctx 以便审计归组
	if session != "" {
		ctx = aictx.WithSessionID(ctx, session)
	}

	// 尝试通过 proxy 执行文件传输
	if proxy := getSSHProxyClient(); proxy != nil {
		exitCode := cmdCpViaProxy(proxy, srcAssetID, srcPath, dstAssetID, dstPath, srcIsRemote, dstIsRemote, src, dst)
		var cpErr error
		if exitCode != 0 {
			cpErr = fmt.Errorf("cp via proxy failed with exit code %d", exitCode)
		}
		writeOpsctlAudit(ctx, "cp", argsJSON, fmt.Sprintf(`{"status":"completed","exit_code":%d}`, exitCode), cpErr, nil)
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
		}, nil)
		return exitCode

	case srcIsRemote && !dstIsRemote:
		// Download: remote -> local
		exitCode := callHandler(ctx, handlers, "download_file", map[string]any{
			"asset_id":    float64(srcAssetID),
			"remote_path": srcPath,
			"local_path":  dst,
		}, nil)
		return exitCode

	default:
		// Asset-to-asset transfer: remote -> remote
		cpErr = helper.CopyBetweenAssets(ctx, srcAssetID, srcPath, dstAssetID, dstPath)
		auditResult := `{"status":"completed"}`
		if cpErr != nil {
			auditResult = fmt.Sprintf(`{"error":%q}`, cpErr.Error())
		}
		writeOpsctlAudit(ctx, "cp", argsJSON, auditResult, cpErr, nil)
		if cpErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", cpErr)
			return 1
		}
		return 0
	}
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
