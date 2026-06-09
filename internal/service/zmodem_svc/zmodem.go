// Package zmodem_svc 提供 ZMODEM(lrzsz) 传输的本地文件 I/O 桥。
//
// ZMODEM 协议本身跑在前端（zmodem.js）：远端 sz/rz 的字节流由前端 Sentry 截获并驱动。
// 后端只负责"协议落到本地磁盘"的那一段——下载时把前端推来的分块写盘，上传时按需把
// 本地文件分块读给前端。进度则复用 internal/pkg/transfer 的 Reporter，与 SFTP 走同一
// 套节流/测速 + "transfer:progress:<id>" 事件管线。
//
// 本包不依赖 Wails runtime（原生对话框留在 binder 层），因此可独立单测。
package zmodem_svc

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/opskat/opskat/internal/pkg/transfer"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// dlHandle 是一次下载（远端 sz）的写盘句柄。
type dlHandle struct {
	f        *os.File
	path     string
	name     string
	size     int64
	written  int64
	reporter *transfer.Reporter
}

// ulHandle 是一次上传（远端 rz）的读盘句柄。
type ulHandle struct {
	f         *os.File
	name      string
	size      int64
	readTotal int64
	reporter  *transfer.Reporter
}

// FileBridge 按 transferID 管理在传的本地文件句柄，并通过注入的 emit 上报进度。
// 同一 transferID 的方法调用由前端的 await 链串行驱动，因此句柄内字段无需加锁；
// mu 仅保护两张 map 自身，使并发的不同传输互不干扰。
type FileBridge struct {
	emit     func(transfer.Progress)
	mu       sync.Mutex
	download map[string]*dlHandle
	upload   map[string]*ulHandle
}

// New 创建文件桥。emit 由 binder 接到 wailsRuntime.EventsEmit("transfer:progress:"+id)。
func New(emit func(transfer.Progress)) *FileBridge {
	return &FileBridge{
		emit:     emit,
		download: make(map[string]*dlHandle),
		upload:   make(map[string]*ulHandle),
	}
}

// --- 下载（远端 sz）---

// BeginDownload 在 path 处创建/截断本地文件并登记下载句柄。path 来自原生 Save 对话框。
func (b *FileBridge) BeginDownload(transferID, path string, size int64) error {
	f, err := os.Create(path) //nolint:gosec // path 来自原生保存对话框
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	h := &dlHandle{
		f:        f,
		path:     path,
		name:     filepath.Base(path),
		size:     size,
		reporter: transfer.NewReporter(b.emit),
	}
	b.mu.Lock()
	b.download[transferID] = h
	b.mu.Unlock()
	return nil
}

// AppendChunk 把一段已接收的字节追加写入下载文件，并上报进度。
func (b *FileBridge) AppendChunk(transferID string, data []byte) error {
	h := b.dl(transferID)
	if h == nil {
		return fmt.Errorf("下载传输不存在: %s", transferID)
	}
	if _, err := h.f.Write(data); err != nil {
		h.reporter.Report(transfer.Progress{
			TransferID: transferID, Status: transfer.StatusError, CurrentFile: h.name, Error: err.Error(),
		})
		return fmt.Errorf("写入本地文件失败: %w", err)
	}
	h.written += int64(len(data))
	h.reporter.Report(transfer.Progress{
		TransferID: transferID, Status: transfer.StatusProgress, CurrentFile: h.name,
		FilesTotal: 1, BytesDone: h.written, BytesTotal: h.size,
	})
	return nil
}

// FinishDownload 关闭下载文件并发出 done。幂等：句柄不存在时静默返回。
func (b *FileBridge) FinishDownload(transferID string) error {
	h := b.takeDL(transferID)
	if h == nil {
		return nil
	}
	if err := h.f.Close(); err != nil {
		h.reporter.Report(transfer.Progress{
			TransferID: transferID, Status: transfer.StatusError, CurrentFile: h.name, Error: err.Error(),
		})
		return fmt.Errorf("关闭本地文件失败: %w", err)
	}
	h.reporter.Report(transfer.Progress{
		TransferID: transferID, Status: transfer.StatusDone, CurrentFile: h.name,
		FilesTotal: 1, FilesCompleted: 1, BytesDone: h.written, BytesTotal: h.size,
	})
	return nil
}

// AbortDownload 关闭并删除半截的下载文件，置为已取消状态。幂等。
func (b *FileBridge) AbortDownload(transferID string) error {
	h := b.takeDL(transferID)
	if h == nil {
		return nil
	}
	if err := h.f.Close(); err != nil {
		logger.Default().Warn("close aborted download", zap.String("path", h.path), zap.Error(err))
	}
	rmErr := os.Remove(h.path)
	h.reporter.Report(transfer.Progress{
		TransferID: transferID, Status: transfer.StatusCancelled, CurrentFile: h.name,
	})
	if rmErr != nil && !os.IsNotExist(rmErr) {
		return fmt.Errorf("删除未完成文件失败: %w", rmErr)
	}
	return nil
}

// --- 上传（远端 rz）---

// OpenUpload 打开 path 供读取并登记上传句柄，返回文件大小与修改时间(Unix 秒)。
// path 来自原生多选对话框。
func (b *FileBridge) OpenUpload(transferID, path string) (size, mtime int64, err error) {
	f, err := os.Open(path) //nolint:gosec // path 来自原生打开对话框
	if err != nil {
		return 0, 0, fmt.Errorf("打开本地文件失败: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			logger.Default().Warn("close upload after stat error", zap.String("path", path), zap.Error(closeErr))
		}
		return 0, 0, fmt.Errorf("获取文件信息失败: %w", err)
	}
	h := &ulHandle{
		f:        f,
		name:     filepath.Base(path),
		size:     info.Size(),
		reporter: transfer.NewReporter(b.emit),
	}
	b.mu.Lock()
	b.upload[transferID] = h
	b.mu.Unlock()
	return info.Size(), info.ModTime().Unix(), nil
}

// ReadChunk 读取至多 n 字节供前端发往远端，并上报进度；返回 eof 表示文件已读完。
func (b *FileBridge) ReadChunk(transferID string, n int) (data []byte, eof bool, err error) {
	h := b.ul(transferID)
	if h == nil {
		return nil, false, fmt.Errorf("上传传输不存在: %s", transferID)
	}
	buf := make([]byte, n)
	read, readErr := h.f.Read(buf)
	if read > 0 {
		h.readTotal += int64(read)
		h.reporter.Report(transfer.Progress{
			TransferID: transferID, Status: transfer.StatusProgress, CurrentFile: h.name,
			FilesTotal: 1, BytesDone: h.readTotal, BytesTotal: h.size,
		})
	}
	if readErr == io.EOF {
		return buf[:read], true, nil
	}
	if readErr != nil {
		h.reporter.Report(transfer.Progress{
			TransferID: transferID, Status: transfer.StatusError, CurrentFile: h.name, Error: readErr.Error(),
		})
		return nil, false, fmt.Errorf("读取本地文件失败: %w", readErr)
	}
	return buf[:read], false, nil
}

// FinishUpload 关闭上传文件并发出 done。幂等。
func (b *FileBridge) FinishUpload(transferID string) error {
	h := b.takeUL(transferID)
	if h == nil {
		return nil
	}
	if err := h.f.Close(); err != nil {
		return fmt.Errorf("关闭本地文件失败: %w", err)
	}
	h.reporter.Report(transfer.Progress{
		TransferID: transferID, Status: transfer.StatusDone, CurrentFile: h.name,
		FilesTotal: 1, FilesCompleted: 1, BytesDone: h.readTotal, BytesTotal: h.size,
	})
	return nil
}

// AbortUpload 关闭上传读句柄（不删除本地源文件），置为已取消状态。幂等。
func (b *FileBridge) AbortUpload(transferID string) error {
	h := b.takeUL(transferID)
	if h == nil {
		return nil
	}
	if err := h.f.Close(); err != nil {
		logger.Default().Warn("close aborted upload", zap.String("file", h.name), zap.Error(err))
	}
	h.reporter.Report(transfer.Progress{
		TransferID: transferID, Status: transfer.StatusCancelled, CurrentFile: h.name,
	})
	return nil
}

// --- 句柄存取小工具 ---

func (b *FileBridge) dl(transferID string) *dlHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.download[transferID]
}

func (b *FileBridge) takeDL(transferID string) *dlHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.download[transferID]
	delete(b.download, transferID)
	return h
}

func (b *FileBridge) ul(transferID string) *ulHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.upload[transferID]
}

func (b *FileBridge) takeUL(transferID string) *ulHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.upload[transferID]
	delete(b.upload, transferID)
	return h
}
