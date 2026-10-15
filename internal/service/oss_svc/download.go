package oss_svc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opskat/opskat/internal/pkg/transfer"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// DownloadPrefixRequest 描述一次"文件夹"下载:把 Prefix 之下的全部对象落到
// DestDir/<Prefix 末段>/ 下并保留层级。
type DownloadPrefixRequest struct {
	AssetID    int64
	Bucket     string
	Prefix     string // 以 "/" 结尾的文件夹前缀
	DestDir    string // 已存在的本地目录
	TransferID string
	OnProgress func(transfer.Progress)
}

// DownloadPrefix 递归下载一个"文件夹"。整次传输是一条进度(文件数与字节数的总量)。
func (s *Service) DownloadPrefix(ctx context.Context, req *DownloadPrefixRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return downloadPrefixWith(ctx, c, req)
}

// downloadEntry 是扫描阶段定下的一项:要建的目录,或要落盘的对象。
type downloadEntry struct {
	key   string
	rel   string // 相对 DestDir 的本地路径
	size  int64
	isDir bool
}

// downloadPrefixWith 分两段:先把整棵前缀扫完(定总量,并把落不进 DestDir 的 key 挡在
// 动笔之前),再按扫描顺序逐个落盘。任一对象失败即中止,已落盘的文件保留。
//
// 写入经 os.Root 限制在 DestDir 之内:对象 key 是任意字符串,而它在这里被当成本地路径。
func downloadPrefixWith(ctx context.Context, c Client, req *DownloadPrefixRequest) error {
	entries, filesTotal, bytesTotal, err := scanPrefix(ctx, c, req.Bucket, req.Prefix)
	if err != nil {
		return err
	}

	logger.Ctx(ctx).Info("oss folder download scanned", zap.String("transferId", req.TransferID),
		zap.Int("files", filesTotal), zap.Int64("bytes", bytesTotal))

	root, err := os.OpenRoot(req.DestDir)
	if err != nil {
		return fmt.Errorf("打开保存目录失败: %w", err)
	}
	defer func() { _ = root.Close() }()

	tracker := transfer.NewTracker(req.TransferID, filesTotal, bytesTotal, req.OnProgress)
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.isDir {
			if err := root.MkdirAll(e.rel, 0o755); err != nil {
				return fmt.Errorf("创建本地目录失败: %w", err)
			}
			continue
		}
		if err := downloadEntryTo(ctx, c, root, tracker, req.Bucket, e); err != nil {
			return err
		}
		tracker.FileDone(e.size)
	}
	return nil
}

// scanPrefix 把 prefix 之下的全部内容展开成落盘清单,文件夹自身排在第一项。
//
// 本地相对路径 = key 去掉文件夹的父前缀,所以文件夹末段成为 DestDir 下的顶层目录。
// 路径必须落在 DestDir 之内且已是规范形态:`..` 会写到目录之外,而 `a//b`、`a/./b`
// 规范化之后会与另一个 key 落到同一路径,后写的悄悄盖掉先写的。
func scanPrefix(ctx context.Context, c Client, bucket, prefix string) ([]downloadEntry, int, int64, error) {
	folder := strings.TrimSuffix(prefix, "/")
	parent := folder[:strings.LastIndex(folder, "/")+1]

	var entries []downloadEntry
	var filesTotal int
	var bytesTotal int64
	add := func(it ObjectItem) error {
		rel := filepath.FromSlash(strings.TrimSuffix(strings.TrimPrefix(it.Key, parent), "/"))
		if !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
			return fmt.Errorf("对象键 %q 无法映射为保存目录内的路径", it.Key)
		}
		entries = append(entries, downloadEntry{key: it.Key, rel: rel, size: it.Size, isDir: it.IsPrefix})
		if !it.IsPrefix {
			filesTotal++
			bytesTotal += it.Size
		}
		return nil
	}

	if err := add(ObjectItem{Key: prefix, IsPrefix: true}); err != nil {
		return nil, 0, 0, err
	}
	list := func(ctx context.Context, p, token string) (*ListObjectsResult, error) {
		return ListObjectsWith(ctx, c, bucket, p, 0, token)
	}
	if err := WalkObjects(ctx, list, prefix, add); err != nil {
		return nil, 0, 0, err
	}
	return entries, filesTotal, bytesTotal, nil
}

func downloadEntryTo(ctx context.Context, c Client, root *os.Root, tracker *transfer.Tracker, bucket string, e downloadEntry) error {
	rc, _, err := c.GetObject(ctx, bucket, e.key)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", e.key, err)
	}
	defer func() { _ = rc.Close() }()

	if err := root.MkdirAll(filepath.Dir(e.rel), 0o755); err != nil {
		return fmt.Errorf("创建本地目录失败: %w", err)
	}
	f, err := root.OpenFile(e.rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	if _, err := io.Copy(tracker.Writer(ctx, f, e.rel), rc); err != nil {
		_ = f.Close()
		return fmt.Errorf("下载 %s 失败: %w", e.key, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("写入本地文件失败: %w", err)
	}
	return nil
}
