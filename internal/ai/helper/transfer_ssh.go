package helper

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

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

func listSFTP(client *sftp.Client, pattern string, recursive bool) (*ListResult, error) {
	if hasGlobMeta(pattern) {
		matches, err := client.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		// sftp.Client.Glob 按服务端 ReadDir 的原始顺序追加（match.go 里的 sort 是注释掉的，
		// 与 filepath.Glob 不同），排序后展开顺序才不随服务端目录序抖动——与 walkSFTP 同一条理由。
		sort.Strings(matches)
		res := &ListResult{}
		base := globBase(pattern)
		var matchedDirs []string
		for _, match := range matches {
			// 只有 glob 这一支需要额外 Lstat：递归那边 ReadDir 已经带回了模式与大小。
			info, err := client.Lstat(match)
			if err != nil {
				return nil, err
			}
			dir, err := appendSFTPInfo(client, match, base, info, recursive, res)
			if err != nil {
				return nil, err
			}
			if dir != "" {
				matchedDirs = append(matchedDirs, dir)
			}
		}
		if len(matchedDirs) > 0 {
			return nil, globMatchedDirsError(pattern, matchedDirs)
		}
		return res, nil
	}

	// 与本地端一致：被指名的单个路径跟随符号链接，跳过只发生在展开途中。
	// 指名一个指向目录的链接时 Stat 交出目标的信息，下面的 ReadDir 同样跟随，两者一致。
	info, err := client.Stat(pattern)
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
	if err := walkSFTP(client, pattern, pattern, res); err != nil {
		return nil, err
	}
	return res, nil
}

// appendSFTPInfo 处理一个被展开出来的路径：符号链接跳过并计数，目录只在递归时下钻，
// 其余是可传输条目。非递归时命中的目录作为第一个返回值交回调用方（空串表示没有），与本地端
// 的 appendLocalPath 同一形状——两端共用 globMatchedDirsError 报出同一句话。
// walkSFTP 永远以 recursive=true 调用，因此那条路上第一个返回值恒为空串。
func appendSFTPInfo(
	client *sftp.Client, p, base string, info os.FileInfo, recursive bool, res *ListResult,
) (matchedDir string, err error) {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		res.SkippedSymlinks = append(res.SkippedSymlinks, p)
	case info.IsDir():
		if recursive {
			return "", walkSFTP(client, p, base, res)
		}
		return p, nil
	default:
		res.Entries = append(res.Entries, Entry{Path: p, RelPath: relTo(base, p), Size: info.Size()})
	}
	return "", nil
}

// walkSFTP 递归展开 dir。ReadDir 是 lstat 语义，符号链接不会被下钻。
// 排序是为了让展开顺序稳定——审批对话框里的条目顺序不该随服务端目录序抖动。
func walkSFTP(client *sftp.Client, dir, base string, res *ListResult) error {
	infos, err := client.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name() < infos[j].Name() })
	for _, info := range infos {
		if _, err := appendSFTPInfo(client, path.Join(dir, info.Name()), base, info, true, res); err != nil {
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
		return writeSFTP(client, p, r)
	})
}

// writeSFTP 在已有的 SFTP 连接上流式写入 p，按需创建父目录。
// 与 List 的 listSFTP 同一形态：连接由调用方管，这里只做那一件事，因此进程内 SFTP
// 服务端就能验证"建父目录 + 字节往返"。
func writeSFTP(client *sftp.Client, p string, r io.Reader) error {
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
}

// ValidateDestination 远端文件系统与本地端同一个答案：没有形态约束（writeSFTP 会
// MkdirAll 父目录），落不下去的原因要真的写才知道。理由见 localAdapter 上的同名方法。
func (sshAdapter) ValidateDestination(string) error { return nil }

// ApprovalSubject：SSH 端点三个方向都归 cp 授权，主体是远端路径本身——与传输面收敛前的
// checkFileTransferPermission + MatchPathRule 逐字节一致。
//
// 例外是 DirList：那一端收到的可能是个 pattern，而主体该说的是**实际会被枚举的那个基点**
// ——用户批准的是"列出这个目录"，主体照抄 pattern 就把范围说成了它命中的那些文件。远端
// 文件系统上 `*?[` 确实是通配语法，因此这里的收窄就是展开基点本身（见
// TransferAdapter.ApprovalSubject 的注释：收窄归适配器）。
//
// 主体里**残留的**元字符不会变成一条通配授权：那一步在 permission.cpGrantPatterns，
// 系统给出的主体落成规则时逐个转义（决策 D21 更正：规则转义、名字原样）。所以这里绝不能
// 自己先转义——路径里的 `*?[` 可以是字面文件名，名字侧转义会让规则匹配不上自己。
func (sshAdapter) ApprovalSubject(p string, dir Direction) (string, string) {
	if dir == DirList && hasGlobMeta(p) {
		return permission.GrantToolCp, globBase(p)
	}
	if dir == DirReadScope || dir == DirWriteScope {
		base := p
		if hasGlobMeta(base) {
			base = globBase(base)
		}
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		return permission.GrantToolCp, base
	}
	return permission.GrantToolCp, p
}
