package oss

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/opskat/opskat/internal/pkg/transfer"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// deriveUploadKey 目标 key = keyPrefix + 本地文件名;非空且不以 "/" 结尾的前缀会补一个 "/",
// 已以 "/" 结尾或为空的前缀保持不变(空前缀 → key 就是文件名本身,上传到 bucket 根)。
func deriveUploadKey(keyPrefix, localPath string) string {
	base := filepath.Base(localPath)
	if keyPrefix == "" {
		return base
	}
	if !strings.HasSuffix(keyPrefix, "/") {
		keyPrefix += "/"
	}
	return keyPrefix + base
}

// contentTypeFor 按扩展名猜测 Content-Type，猜不出时回退为通用二进制流。
func contentTypeFor(localPath string) string {
	if ct := mime.TypeByExtension(filepath.Ext(localPath)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func (o *OSS) emitProgress(transferID string, p transfer.Progress) {
	wailsRuntime.EventsEmit(o.ctx, "transfer:progress:"+transferID, p)
}

// emitTerminal 依据 err 发出 done/error/取消 终态(立即,不节流)。
func (o *OSS) emitTerminal(transferID string, err error) {
	switch {
	case err == nil:
		logger.Ctx(o.ctx).Info("oss transfer end", zap.String("transferId", transferID))
		o.emitProgress(transferID, transfer.Progress{TransferID: transferID, Status: transfer.StatusDone})
	case errors.Is(err, context.Canceled):
		logger.Ctx(o.ctx).Info("oss transfer canceled", zap.String("transferId", transferID))
		o.emitProgress(transferID, transfer.Progress{TransferID: transferID, Status: transfer.StatusCancelled})
	default:
		logger.Ctx(o.ctx).Error("oss transfer fail", zap.String("transferId", transferID), zap.Error(err))
		o.emitProgress(transferID, transfer.Progress{TransferID: transferID, Status: transfer.StatusError, Error: err.Error()})
	}
}

// OSSUploadObject 弹原生多选对话框,对每个选中文件起一路流式上传,返回各 transferID。
// 用户取消对话框 → 返回空切片。
func (o *OSS) OSSUploadObject(assetID int64, bucket, keyPrefix string) ([]string, error) {
	ctx := o.i18nCtx()
	logger.Ctx(ctx).Info("oss upload selection start", zap.Int64("assetId", assetID), zap.String("bucket", bucket))
	if assetID <= 0 || bucket == "" {
		err := fmt.Errorf("invalid request: assetID and bucket are required")
		logger.Ctx(ctx).Error("oss upload selection fail", zap.Int64("assetId", assetID), zap.Error(err))
		return nil, err
	}
	localPaths, err := wailsRuntime.OpenMultipleFilesDialog(o.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件",
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if len(localPaths) == 0 {
		return []string{}, nil // 用户取消
	}

	ids := make([]string, 0, len(localPaths))
	for _, localPath := range localPaths {
		transferID := transfer.GenerateID("oss")
		key := deriveUploadKey(keyPrefix, localPath)
		o.pending.Store(transferID, func() { o.uploadObject(transferID, assetID, bucket, key, localPath) })
		ids = append(ids, transferID)
	}
	logger.Ctx(ctx).Info("oss upload selection end", zap.Int64("assetId", assetID), zap.Int("count", len(ids)))
	return ids, nil
}

// OSSUploadObjectPath 无对话框(拖拽遮罩用),流式上传单个本地文件到指定 key。
func (o *OSS) OSSUploadObjectPath(assetID int64, bucket, key, localPath string) (string, error) {
	if assetID <= 0 || bucket == "" || key == "" || localPath == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket, key and localPath are required")
	}
	transferID := transfer.GenerateID("oss")
	o.pending.Store(transferID, func() { o.uploadObject(transferID, assetID, bucket, key, localPath) })
	return transferID, nil
}

// OSSStartTransfer 在前端创建进度行并完成 EventsOn 订阅后启动已准备的传输。
func (o *OSS) OSSStartTransfer(transferID string) error {
	if transferID == "" {
		return fmt.Errorf("invalid transferID")
	}
	v, ok := o.pending.LoadAndDelete(transferID)
	if !ok {
		return fmt.Errorf("transfer not found: %s", transferID)
	}
	logger.Ctx(o.i18nCtx()).Info("oss transfer start", zap.String("transferId", transferID))
	go v.(func())()
	return nil
}

// uploadObject 单文件流式上传:注册取消 → 打开文件 → 包进度 reader → service.PutObject → emit 终态。
// 运行在独立 goroutine 中,recover 是这条 goroutine 的边界防线:任何未预料的 panic 都不能打垮整个应用,
// 必须记录日志并把这路传输标记为 error 终态,而不是让前端的进度条永远悬空。
func (o *OSS) uploadObject(transferID string, assetID int64, bucket, key, localPath string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(o.ctx).Error("oss upload panic recovered",
				zap.String("transferId", transferID), zap.Any("panic", r), zap.Stack("stack"))
			o.emitTerminal(transferID, fmt.Errorf("上传发生意外错误: %v", r))
		}
	}()

	ctx, cancel := context.WithCancel(o.ctx)
	o.cancels.Store(transferID, cancel)
	defer func() {
		o.cancels.Delete(transferID)
		cancel()
	}()

	f, err := os.Open(localPath) //nolint:gosec // path 来自用户文件对话框/拖拽
	if err != nil {
		o.emitTerminal(transferID, fmt.Errorf("打开本地文件失败: %w", err))
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		o.emitTerminal(transferID, fmt.Errorf("获取文件信息失败: %w", err))
		return
	}

	pr := transfer.NewProgressReader(ctx, transferID, filepath.Base(localPath), f, info.Size(), func(p transfer.Progress) {
		o.emitProgress(transferID, p)
	})
	err = o.service.PutObject(ctx, assetID, bucket, key, pr, info.Size(), contentTypeFor(localPath))
	o.emitTerminal(transferID, err)
}

// OSSDownloadObject 弹原生保存对话框(默认名 = key 末段),流式下载并报进度,返回 transferID。
// 用户取消对话框 → 返回空串。
func (o *OSS) OSSDownloadObject(assetID int64, bucket, key string) (string, error) {
	if assetID <= 0 || bucket == "" || key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	localPath, err := wailsRuntime.SaveFileDialog(o.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: path.Base(key),
		Title:           "保存到本地",
	})
	if err != nil {
		return "", fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if localPath == "" {
		return "", nil // 用户取消
	}
	transferID := transfer.GenerateID("oss")
	o.pending.Store(transferID, func() { o.downloadObject(transferID, assetID, bucket, key, localPath) })
	return transferID, nil
}

// downloadObject 单对象流式下载:注册取消 → GetObject 拿流 → 建本地文件 → transfer.Copy → emit 终态。
// 运行在独立 goroutine 中,recover 语义同 uploadObject:边界防线,记录日志并把这路传输标记为 error 终态。
func (o *OSS) downloadObject(transferID string, assetID int64, bucket, key, localPath string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(o.ctx).Error("oss download panic recovered",
				zap.String("transferId", transferID), zap.Any("panic", r), zap.Stack("stack"))
			o.emitTerminal(transferID, fmt.Errorf("下载发生意外错误: %v", r))
		}
	}()

	ctx, cancel := context.WithCancel(o.ctx)
	o.cancels.Store(transferID, cancel)
	defer func() {
		o.cancels.Delete(transferID)
		cancel()
	}()

	rc, size, err := o.service.GetObject(ctx, assetID, bucket, key)
	if err != nil {
		o.emitTerminal(transferID, err)
		return
	}
	defer func() { _ = rc.Close() }()

	f, err := os.Create(localPath) //nolint:gosec // path 来自保存对话框
	if err != nil {
		o.emitTerminal(transferID, fmt.Errorf("创建本地文件失败: %w", err))
		return
	}
	defer func() { _ = f.Close() }()

	err = transfer.Copy(ctx, transferID, f, rc, size, path.Base(key), func(p transfer.Progress) {
		o.emitProgress(transferID, p)
	})
	o.emitTerminal(transferID, err)
}

// OSSCancelTransfer 经注册表取消在途上传/下载(命中即调用其 CancelFunc,ctx 取消触发取消终态);
// 未命中(已终结或从未存在的 transferID)视为幂等 no-op,不报错。
func (o *OSS) OSSCancelTransfer(transferID string) error {
	if transferID == "" {
		return fmt.Errorf("invalid transferID")
	}
	if v, ok := o.cancels.Load(transferID); ok {
		v.(context.CancelFunc)()
	} else if _, ok := o.pending.LoadAndDelete(transferID); ok {
		o.emitTerminal(transferID, context.Canceled)
	}
	return nil
}
