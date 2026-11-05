package oss_svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opskat/opskat/internal/pkg/transfer"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
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

// folderDownloadWorkers 是文件夹下载同时在传的对象数上限(与 SFTP 目录传输一致)。
// 小对象的耗时几乎全是请求往返,多个对象同时在传才能把这些等待叠起来。
const folderDownloadWorkers = 8

// downloadPrefixWith 分两段:先把整棵前缀扫完(定总量,并把落不进 DestDir 的 key 挡在
// 动笔之前),再按扫描顺序派发:目录由派发方当场建立,对象交给最多 folderDownloadWorkers
// 个 worker 并发落盘。第一个失败的对象取消其余对象,其错误作为返回值;ctx 取消时所有
// worker 随之停止。返回前等待全部 worker 退出,已落盘的文件保留。
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
	dispatchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	g, workerCtx := errgroup.WithContext(dispatchCtx)
	g.SetLimit(folderDownloadWorkers)
	var mkdirErr error
	for _, e := range entries {
		if workerCtx.Err() != nil {
			break
		}
		if e.isDir {
			if mkdirErr = root.MkdirAll(e.rel, 0o755); mkdirErr != nil {
				mkdirErr = fmt.Errorf("创建本地目录失败: %w", mkdirErr)
				cancel()
				break
			}
			continue
		}
		g.Go(func() error {
			// g.Go 在 worker 占满时会等空位,等到时传输可能已被取消或有对象失败。
			if err := workerCtx.Err(); err != nil {
				return err
			}
			err := downloadEntryTo(workerCtx, c, root, tracker.File(e.rel, e.size), req.Bucket, e)
			if err != nil && !errors.Is(err, context.Canceled) {
				// 只有第一个错误会被返回,其余对象各自的失败原因记在日志里。
				logger.Ctx(ctx).Warn("oss folder download object failed", zap.String("transferId", req.TransferID),
					zap.String("key", e.key), zap.Error(err))
			}
			return err
		})
	}
	waitErr := g.Wait()
	if mkdirErr != nil {
		return mkdirErr
	}
	if waitErr != nil {
		return waitErr
	}
	// 取消发生在两个对象之间时可能没有 worker 报错,仍要把取消报给调用方。
	return ctx.Err()
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

func downloadEntryTo(ctx context.Context, c Client, root *os.Root, file *transfer.TrackedFile, bucket string, e downloadEntry) error {
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
	if _, err := io.Copy(file.Writer(ctx, f), rc); err != nil {
		_ = f.Close()
		return fmt.Errorf("下载 %s 失败: %w", e.key, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("写入本地文件失败: %w", err)
	}
	file.Done()
	return nil
}
