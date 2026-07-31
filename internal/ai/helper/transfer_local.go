package helper

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// localAdapter 是本地端点。它没有资产，因此不进注册表也不产生审批项，
// 由 TransferAdapterFor(nil) 交出。
type localAdapter struct{}

var localTransfer TransferAdapter = localAdapter{}

func (localAdapter) List(
	_ context.Context, _ *asset_entity.Asset, pattern string, recursive bool,
) (*ListResult, error) {
	if hasGlobMeta(pattern) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		res := &ListResult{}
		base := globBase(filepath.ToSlash(pattern))
		var matchedDirs []string
		for _, match := range matches {
			dir, err := appendLocalPath(match, base, recursive, res)
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

	// 被指名的单个路径跟随符号链接（POSIX cp 的行为）；跳过只发生在展开途中，
	// 那里的路径是被枚举出来的、不在用户审阅过的清单里。
	info, err := os.Stat(pattern)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return &ListResult{Entries: []Entry{{Path: pattern, RelPath: info.Name(), Size: info.Size()}}}, nil
	}
	if !recursive {
		return nil, fmt.Errorf("%q is a directory; set recursive to transfer it", pattern)
	}
	// 上面的 Stat 已经跟随过链接，WalkDir 却只 Lstat 根：直接把一条指名的目录链接走进去，
	// 根会被当成"展开途中的链接"跳过、交出零条目。先解析到目标再走，两者语义才一致
	// （SSH 端的 ReadDir 对被指名的路径同样跟随）。
	root, err := resolveNamedDir(pattern)
	if err != nil {
		return nil, err
	}
	res := &ListResult{}
	if err := walkLocal(root, filepath.ToSlash(root), res); err != nil {
		return nil, err
	}
	return res, nil
}

// resolveNamedDir 把被指名的目录路径解析成实际要遍历的根：只有它自己是符号链接时才解析，
// 其余原样返回——无条件 EvalSymlinks 会把中间层的链接也一并改写（macOS 上 /tmp/x 会变成
// /private/tmp/x），让展开出的 Path 与用户输入对不上。
func resolveNamedDir(p string) (string, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return p, nil
	}
	return filepath.EvalSymlinks(p)
}

// appendLocalPath 处理一个被展开出来的路径：符号链接跳过并计数，目录只在递归时下钻，
// 其余是可传输条目。非递归时命中的目录作为第一个返回值交回调用方（空串表示没有），
// 由它一次点名报错——见 globMatchedDirsError。
func appendLocalPath(p, base string, recursive bool, res *ListResult) (matchedDir string, err error) {
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		res.SkippedSymlinks = append(res.SkippedSymlinks, p)
	case info.IsDir():
		if recursive {
			return "", walkLocal(p, base, res)
		}
		return p, nil
	default:
		res.Entries = append(res.Entries, Entry{
			Path: p, RelPath: relTo(base, filepath.ToSlash(p)), Size: info.Size(),
		})
	}
	return "", nil
}

// globMatchedDirsError 是本地与 SSH 两端共用的答复：非递归的 glob 命中了目录。两端逐字节
// 同一句话——换个端点不该换个解释；语气与 transfer_oss.go 里前缀那一支一致（对象存储没有
// 目录实体，同一件事在那边表现为命中公共前缀）。
//
// 报错而不是略过，是因为同一个 List 里"指名目录却没有 recursive"那一支早就是报错：只因用户
// 打了个 '*' 就换个答案，是没人选过的不一致。POSIX cp 也不站在略过那一边——`cp src/* dst/`
// 会打 "cp: src/sub is a directory (not copied)" 并以非零退出；静默略过换来的是一次看起来
// 成功、实际只传了一部分的传输（spec D19 否决静默截断）。
func globMatchedDirsError(pattern string, dirs []string) error {
	return fmt.Errorf("pattern %q matches directories (%s); set recursive to transfer them",
		pattern, strings.Join(dirs, ", "))
}

// walkLocal 递归展开 root 下的文件。WalkDir 本身不跟随符号链接，链接项在这里被跳过计数。
func walkLocal(root, base string, res *ListResult) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			res.SkippedSymlinks = append(res.SkippedSymlinks, p)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		res.Entries = append(res.Entries, Entry{
			Path: p, RelPath: relTo(base, filepath.ToSlash(p)), Size: info.Size(),
		})
		return nil
	})
}

func (localAdapter) OpenRead(
	_ context.Context, _ *asset_entity.Asset, path string,
) (io.ReadCloser, int64, error) {
	f, err := os.Open(path) //nolint:gosec // 路径来自已经过端点审批的传输清单
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open local file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		closeLocalFile(f, path)
		return nil, 0, fmt.Errorf("failed to stat local file: %w", err)
	}
	return f, info.Size(), nil
}

func (localAdapter) Write(
	_ context.Context, _ *asset_entity.Asset, path string, r io.Reader, _ int64,
) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}
	f, err := os.Create(path) //nolint:gosec // 同上：目的路径已经过端点审批
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer closeLocalFile(f, path)
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("failed to write local file: %w", err)
	}
	return nil
}

// ValidateDestination 本地文件系统对写入目标没有形态约束：Write 会按需建父目录，
// 任何路径都可能是一个待创建的文件。剩下的（父目录是个文件、没有写权限）要真的写才
// 知道，在这里探测一次只会把一条 TOCTOU 缝塞进审批与写入之间——那是 D16 否决目的地
// 推断时给的同一个理由。
func (localAdapter) ValidateDestination(string) error { return nil }

// ApprovalSubject 见 TransferAdapter 的注释：本地端没有资产，也就没有审批主体。
func (localAdapter) ApprovalSubject(string, Direction) (string, string) {
	return "", ""
}

func closeLocalFile(f *os.File, path string) {
	if err := f.Close(); err != nil && !IsExpectedCloseErr(err) {
		logger.Default().Warn("close local file", zap.String("path", path), zap.Error(err))
	}
}
