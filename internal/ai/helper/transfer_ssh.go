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
	"github.com/opskat/opskat/internal/pkg/sftpio"
)

// sshAdapter 是 SSH 端点，走 SFTP。
type sshAdapter struct{}

var sshTransfer TransferAdapter = sshAdapter{}

func (sshAdapter) NormalizeTransferPath(p string) string {
	trailing := strings.HasSuffix(p, "/")
	p = path.Clean(p)
	if trailing && p != "/" {
		p += "/"
	}
	return p
}

func (sshAdapter) ResolveTransferPath(
	ctx context.Context, asset *asset_entity.Asset, p string,
) (string, error) {
	var home string
	err := ExecuteWithSFTP(ctx, asset.ID, func(client *sftp.Client) error {
		var getwdErr error
		home, getwdErr = client.Getwd()
		return getwdErr
	})
	if err != nil {
		return "", fmt.Errorf("resolve remote home for asset %q: %w", asset.Name, err)
	}
	if !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("resolve remote home for asset %q: SFTP returned non-absolute path %q", asset.Name, home)
	}
	if p == "~" {
		return home, nil
	}
	resolved := path.Join(home, strings.TrimPrefix(p, "~/"))
	if strings.HasSuffix(p, "/") && resolved != "/" {
		resolved += "/"
	}
	return resolved, nil
}

func (sshAdapter) SameAssetCopyHint(string) string { return "" }

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
		if err := res.appendEntry(Entry{Path: p, RelPath: relTo(base, p), Size: info.Size()}); err != nil {
			return "", err
		}
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
	prepared := preparedSFTPDirsFor(ctx, asset.ID)
	return ExecuteWithSFTP(ctx, asset.ID, func(client *sftp.Client) error {
		return writeSFTP(ctx, client, p, r, prepared)
	})
}

// sftpDirSet 是一次多条目传输里已经建好、核验过的目的目录（path.Clean 后的路径）：都在
// 已批准范围之内，从范围往下每一级都是真目录、不是符号链接。只在 PrepareTransferDirs 里
// 写入，之后被并发的 Write 只读共用。
type sftpDirSet map[string]struct{}

type sftpPreparedDirs struct {
	assetID int64
	dirs    sftpDirSet
}

type sftpPreparedDirsKey struct{}

// PrepareTransferDirs 在写入开始前按 paths 的顺序把目的目录逐级建好：每个目录只 Lstat 一次、
// 不存在才 Mkdir 一次。随后的 Write 对落在这些目录里的文件只再检查文件名自己那一级——
// 原来每个文件都要把范围和每一级分量重新 Lstat 一遍，再 MkdirAll 一次。
func (sshAdapter) PrepareTransferDirs(
	ctx context.Context, asset *asset_entity.Asset, paths []string,
) (context.Context, error) {
	var dirs sftpDirSet
	err := ExecuteWithSFTP(ctx, asset.ID, func(client *sftp.Client) error {
		var prepErr error
		dirs, prepErr = prepareSFTPDirs(client, transferWriteScope(ctx), paths)
		return prepErr
	})
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, sftpPreparedDirsKey{}, &sftpPreparedDirs{assetID: asset.ID, dirs: dirs}), nil
}

func preparedSFTPDirsFor(ctx context.Context, assetID int64) sftpDirSet {
	prepared, _ := ctx.Value(sftpPreparedDirsKey{}).(*sftpPreparedDirs)
	if prepared == nil || prepared.assetID != assetID {
		return nil
	}
	return prepared.dirs
}

// prepareSFTPDirs 是 PrepareTransferDirs 在一条已有连接上的那一步。核验语义与
// validateSFTPWritePath 相同（越出范围、范围本身或任一级目录是符号链接、某一级不是目录都拒绝），
// 只是每个目录只核验一次。
func prepareSFTPDirs(client *sftp.Client, scope string, paths []string) (sftpDirSet, error) {
	if scope == "" {
		return nil, fmt.Errorf("preparing remote destination directories requires an approved write scope")
	}
	cleanScope := path.Clean(scope)
	exists, err := checkSFTPWriteScope(client, scope)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := client.MkdirAll(cleanScope); err != nil {
			return nil, fmt.Errorf("failed to create remote directory: %w", err)
		}
	}
	dirs := sftpDirSet{cleanScope: {}}
	for _, p := range paths {
		clean := path.Clean(p)
		if !sftpPathWithin(cleanScope, clean) {
			return nil, fmt.Errorf("remote destination %q escapes approved scope %q", p, scope)
		}
		if clean == cleanScope {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path.Dir(clean), cleanScope), "/")
		if rel == "" {
			continue
		}
		current := cleanScope
		for _, part := range strings.Split(rel, "/") {
			current = path.Join(current, part)
			if _, done := dirs[current]; done {
				continue
			}
			exists, err := lstatSFTPComponent(client, p, current, true)
			if err != nil {
				return nil, err
			}
			if !exists {
				if err := client.Mkdir(current); err != nil {
					return nil, fmt.Errorf("failed to create remote directory %q: %w", current, err)
				}
			}
			dirs[current] = struct{}{}
		}
	}
	return dirs, nil
}

// writeSFTP 在已有的 SFTP 连接上流式写入 p，按需创建父目录。
// 与 List 的 listSFTP 同一形态：连接由调用方管，这里只做那一件事，因此进程内 SFTP
// 服务端就能验证"建父目录 + 字节往返"。prepared 是 PrepareTransferDirs 已经建好并核验过
// 的目录，父目录在其中时跳过 MkdirAll 与逐级核验；为 nil 时逐文件处理。
func writeSFTP(ctx context.Context, client *sftp.Client, p string, r io.Reader, prepared sftpDirSet) error {
	if err := validateSFTPWritePath(ctx, client, p, prepared); err != nil {
		return err
	}
	if _, ok := prepared[path.Dir(path.Clean(p))]; !ok {
		if err := client.MkdirAll(path.Dir(p)); err != nil {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
	}
	f, err := client.Create(p)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			if err := f.Close(); err != nil && !IsExpectedCloseErr(err) {
				logger.Ctx(ctx).Warn("close remote file", zap.String("path", p), zap.Error(err))
			}
		}
	}()
	if _, writeErr := sftpio.Upload(f, r); writeErr != nil {
		// 并发写中途失败会在目标文件里留下空洞，长度却看着正常 —— 必须把半成品删掉，
		// 否则用户拿到的是一个"看起来传完了"的坏文件。这是用 sftpio 的前置条件。
		if closeErr := f.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Ctx(ctx).Warn("close remote file after failed write", zap.String("path", p), zap.Error(closeErr))
		}
		closed = true
		if rmErr := client.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			logger.Ctx(ctx).Warn("cleanup partial remote file", zap.String("path", p), zap.Error(rmErr))
		}
		return fmt.Errorf("failed to write remote file: %w", writeErr)
	}
	if err := f.Close(); err != nil {
		closed = true
		return fmt.Errorf("failed to close remote file: %w", err)
	}
	closed = true
	return nil
}

// validateSFTPWritePath 拒绝越出已批准范围、或经过符号链接的写入目标。父目录已在 prepared
// 里时，范围与各级目录已经核验过，只剩文件名那一级：它本身是符号链接照样拒绝。
func validateSFTPWritePath(ctx context.Context, client *sftp.Client, p string, prepared sftpDirSet) error {
	scope := transferWriteScope(ctx)
	clean := path.Clean(p)
	if scope != "" && !sftpPathWithin(path.Clean(scope), clean) {
		return fmt.Errorf("remote destination %q escapes approved scope %q", p, scope)
	}
	if _, ok := prepared[path.Dir(clean)]; ok {
		_, err := lstatSFTPComponent(client, p, clean, false)
		return err
	}
	if scope != "" {
		if _, err := checkSFTPWriteScope(client, scope); err != nil {
			return err
		}
	}
	current := path.Dir(clean)
	parts := []string{path.Base(clean)}
	if scope != "" {
		cleanScope := path.Clean(scope)
		current = cleanScope
		rel := strings.TrimPrefix(strings.TrimPrefix(clean, cleanScope), "/")
		parts = strings.Split(rel, "/")
	}
	for i, part := range parts {
		current = path.Join(current, part)
		if _, err := lstatSFTPComponent(client, p, current, i < len(parts)-1); err != nil {
			return err
		}
	}
	return nil
}

// sftpPathWithin 报告 clean 是否就是 cleanScope 或落在它之下（两者都已 path.Clean）。
func sftpPathWithin(cleanScope, clean string) bool {
	return clean == cleanScope || strings.HasPrefix(clean, strings.TrimSuffix(cleanScope, "/")+"/")
}

// checkSFTPWriteScope 核验已批准的目的范围本身：不得是符号链接；不存在不是错误，由
// exists=false 交回。
func checkSFTPWriteScope(client *sftp.Client, scope string) (exists bool, err error) {
	info, err := client.Lstat(path.Clean(scope))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect remote destination scope %q: %w", scope, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("remote destination scope %q is a symbolic link", scope)
	}
	return true, nil
}

// lstatSFTPComponent 核验写入目标 p 路径上的一级 current：不得是符号链接；mustBeDir 时存在
// 就必须是目录。不存在不是错误，由 exists=false 交回。
func lstatSFTPComponent(client *sftp.Client, p, current string, mustBeDir bool) (exists bool, err error) {
	info, err := client.Lstat(current)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect remote destination %q: %w", current, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("remote destination %q contains symbolic link %q", p, current)
	}
	if mustBeDir && !info.IsDir() {
		return true, fmt.Errorf("remote destination parent %q is not a directory", current)
	}
	return true, nil
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
	approvalType := permission.GrantToolCpRead
	if dir == DirWrite || dir == DirWriteScope {
		approvalType = permission.GrantToolCpWrite
	}
	if dir == DirList && hasGlobMeta(p) {
		return approvalType, globBase(p)
	}
	if dir == DirReadScope || dir == DirWriteScope {
		base := p
		if hasGlobMeta(base) {
			base = globBase(base)
		}
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		return approvalType, base
	}
	return approvalType, p
}

func (a sshAdapter) ApprovalSubjects(p string, dir Direction) []ApprovalTarget {
	if dir == DirReadScope && !hasGlobMeta(p) && !strings.HasSuffix(p, "/") {
		return []ApprovalTarget{
			{ApprovalType: permission.GrantToolCpRead, Subject: p},
			{ApprovalType: permission.GrantToolCpRead, Subject: p + "/"},
		}
	}
	typ, subject := a.ApprovalSubject(p, dir)
	return []ApprovalTarget{{ApprovalType: typ, Subject: subject}}
}
