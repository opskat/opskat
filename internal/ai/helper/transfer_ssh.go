package helper

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/pkg/sftp"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// sshAdapter 是 SSH 端点，走 SFTP。
type sshAdapter struct{}

var sshTransfer TransferAdapter = sshAdapter{}

func init() {
	RegisterTransferAdapter(asset_entity.AssetTypeSSH, sshTransfer)
}

// sftpFS 是展开所需的 *sftp.Client 子集。抽出接口是因为仓内没有 SFTP 测试服务器，
// 而展开规则（glob 基点、递归基点、symlink 跳过）必须能被验证。
type sftpFS interface {
	Glob(pattern string) ([]string, error)
	ReadDir(p string) ([]os.FileInfo, error)
	Lstat(p string) (os.FileInfo, error)
	Stat(p string) (os.FileInfo, error)
}

func (sshAdapter) List(
	ctx context.Context, asset *asset_entity.Asset, pattern string, recursive bool,
) (*ListResult, error) {
	var res *ListResult
	err := ExecuteWithSFTP(ctx, asset.ID, func(client *sftp.Client) error {
		var listErr error
		res, listErr = listSFTP(client, pattern, recursive)
		return listErr
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func listSFTP(fsys sftpFS, pattern string, recursive bool) (*ListResult, error) {
	if hasGlobMeta(pattern) {
		matches, err := fsys.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		res := &ListResult{}
		base := globBase(pattern)
		for _, match := range matches {
			// 只有 glob 这一支需要额外 Lstat：递归那边 ReadDir 已经带回了模式与大小。
			info, err := fsys.Lstat(match)
			if err != nil {
				return nil, err
			}
			if err := appendSFTPInfo(fsys, match, base, info, recursive, res); err != nil {
				return nil, err
			}
		}
		return res, nil
	}

	// 与本地端一致：被指名的单个路径跟随符号链接，跳过只发生在展开途中。
	info, err := fsys.Stat(pattern)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return &ListResult{Entries: []Entry{{Path: pattern, RelPath: info.Name(), Size: info.Size()}}}, nil
	}
	if !recursive {
		return nil, fmt.Errorf("%q is a directory; set recursive to transfer it", pattern)
	}
	res := &ListResult{}
	if err := walkSFTP(fsys, pattern, pattern, res); err != nil {
		return nil, err
	}
	return res, nil
}

// appendSFTPInfo 处理一个被展开出来的路径：符号链接跳过并计数，目录只在递归时下钻，
// 其余是可传输条目。
func appendSFTPInfo(
	fsys sftpFS, p, base string, info os.FileInfo, recursive bool, res *ListResult,
) error {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		res.SkippedSymlinks = append(res.SkippedSymlinks, p)
	case info.IsDir():
		if recursive {
			return walkSFTP(fsys, p, base, res)
		}
		// 非递归时目录不是可传输条目，与本地端一致。
	default:
		res.Entries = append(res.Entries, Entry{Path: p, RelPath: relTo(base, p), Size: info.Size()})
	}
	return nil
}

// walkSFTP 递归展开 dir。ReadDir 是 lstat 语义，符号链接不会被下钻。
// 排序是为了让展开顺序稳定——审批对话框里的条目顺序不该随服务端目录序抖动。
func walkSFTP(fsys sftpFS, dir, base string, res *ListResult) error {
	infos, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name() < infos[j].Name() })
	for _, info := range infos {
		if err := appendSFTPInfo(fsys, path.Join(dir, info.Name()), base, info, true, res); err != nil {
			return err
		}
	}
	return nil
}

func (sshAdapter) OpenRead(
	ctx context.Context, asset *asset_entity.Asset, p string,
) (io.ReadCloser, int64, error) {
	return openSFTPFile(ctx, asset.ID, p)
}

func (sshAdapter) Write(
	ctx context.Context, asset *asset_entity.Asset, p string, r io.Reader, _ int64,
) error {
	return ExecuteWithSFTP(ctx, asset.ID, func(client *sftp.Client) error {
		if err := client.MkdirAll(path.Dir(p)); err != nil {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
		f, err := client.Create(p)
		if err != nil {
			return fmt.Errorf("failed to create remote file: %w", err)
		}
		defer func() {
			if err := f.Close(); err != nil && !IsExpectedCloseErr(err) {
				logger.Default().Warn("close remote file", zap.String("path", p), zap.Error(err))
			}
		}()
		if _, err := io.Copy(f, r); err != nil {
			return fmt.Errorf("failed to write remote file: %w", err)
		}
		return nil
	})
}

// ApprovalSubject：SSH 端点三个方向都归 cp 授权，主体是远端路径本身——与传输面收敛前的
// checkFileTransferPermission + MatchPathRule 逐字节一致。
func (sshAdapter) ApprovalSubject(p string, _ Direction) (string, string) {
	return permission.GrantToolCp, p
}
