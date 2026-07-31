package helper

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

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
		for _, match := range matches {
			if err := appendLocalPath(match, base, recursive, res); err != nil {
				return nil, err
			}
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
	res := &ListResult{}
	if err := walkLocal(pattern, filepath.ToSlash(pattern), res); err != nil {
		return nil, err
	}
	return res, nil
}

// appendLocalPath 处理一个被展开出来的路径：符号链接跳过并计数，目录只在递归时下钻，
// 其余是可传输条目。
func appendLocalPath(p, base string, recursive bool, res *ListResult) error {
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		res.SkippedSymlinks = append(res.SkippedSymlinks, p)
	case info.IsDir():
		if recursive {
			return walkLocal(p, base, res)
		}
		// 非递归时目录不是可传输条目：'*' 命中的目录直接略过，与 POSIX cp 一致。
	default:
		res.Entries = append(res.Entries, Entry{
			Path: p, RelPath: relTo(base, filepath.ToSlash(p)), Size: info.Size(),
		})
	}
	return nil
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

// ApprovalSubject 见 TransferAdapter 的注释：本地端没有资产，也就没有审批主体。
func (localAdapter) ApprovalSubject(string, Direction) (string, string) {
	return "", ""
}

func closeLocalFile(f *os.File, path string) {
	if err := f.Close(); err != nil && !IsExpectedCloseErr(err) {
		logger.Default().Warn("close local file", zap.String("path", path), zap.Error(err))
	}
}
