// Package transfer 提供文件传输的通用进度原语：进度事件结构、唯一传输 ID 生成、
// 以及"节流 + 测速"的进度上报器。SFTP、ZMODEM(lrzsz) 等所有文件传输共用一套，
// 避免各自重复实现节流/测速逻辑，也让前端订阅同一份进度事件形状。
package transfer

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// 传输状态。值与前端 SFTPTransfer.status 枚举对齐，是前后端的唯一约定来源。
const (
	StatusProgress  = "progress"
	StatusDone      = "done"
	StatusError     = "error"
	StatusCancelled = "cancelled" //nolint:misspell // 与前端 SFTPTransfer.status 枚举对齐（沿用英式拼写）
)

// Progress 是一次传输的进度快照，经 Wails 事件 "transfer:progress:<id>" 发往前端。
// JSON tag 与前端 SFTPTransfer 一一对应，新增传输来源（如 ZMODEM）直接复用。
type Progress struct {
	TransferID     string `json:"transferId"`
	Status         string `json:"status"` // "progress" | "done" | "error"
	CurrentFile    string `json:"currentFile"`
	FilesCompleted int    `json:"filesCompleted"`
	FilesTotal     int    `json:"filesTotal"`
	BytesDone      int64  `json:"bytesDone"`
	BytesTotal     int64  `json:"bytesTotal"`
	Speed          int64  `json:"speed"` // bytes/sec
	Error          string `json:"error,omitempty"`
}

var idCounter atomic.Int64

// GenerateID 生成全局唯一的传输 ID，prefix 标识来源（"sftp" / "zmodem"），
// 便于日志溯源与前端按前缀区分。
func GenerateID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), idCounter.Add(1))
}

const defaultMinInterval = 100 * time.Millisecond

// Reporter 把高频的字节进度收敛成"每 100ms 一条 + 整体平均速率"的进度事件。
// 每个传输持有独立 Reporter；同一传输的 Report 调用必须串行（拷贝循环 / 单链 await
// 天然满足），因此内部不加锁。"done"/"error" 等终态不受节流影响、立即发出。
type Reporter struct {
	emit        func(Progress)
	now         func() time.Time
	start       time.Time
	lastEmit    time.Time // 零值表示尚未发过 progress，首条立即放行
	minInterval time.Duration
}

// NewReporter 创建一个以真实时钟计时的进度上报器。
func NewReporter(emit func(Progress)) *Reporter {
	return newReporter(emit, time.Now)
}

// newReporter 允许注入时钟，便于在测试里确定性地验证节流与测速。
func newReporter(emit func(Progress), now func() time.Time) *Reporter {
	return &Reporter{
		emit:        emit,
		now:         now,
		start:       now(),
		minInterval: defaultMinInterval,
	}
}

// Report 上报一次进度。"progress" 状态按 minInterval 节流并补齐平均速率；
// 其余终态（done/error）立即发出。
func (r *Reporter) Report(p Progress) {
	if p.Status != "progress" {
		r.emit(p)
		return
	}

	now := r.now()
	if !r.lastEmit.IsZero() && now.Sub(r.lastEmit) < r.minInterval {
		return
	}
	r.lastEmit = now

	if elapsed := now.Sub(r.start).Seconds(); elapsed > 0 {
		p.Speed = int64(float64(p.BytesDone) / elapsed)
	}
	r.emit(p)
}

// Tracker 跟踪一次传输的整体进度。一次传输可能包含多个文件（目录上传/下载），
// 总文件数与总字节数在扫描阶段一次确定，之后每个文件只需上报"当前文件已传字节"，
// 累计量由 Tracker 维护 —— 调用方不再需要把 base/total 这类跨文件状态逐层透传，
// 那正是把"当前文件大小"误当成整次总量的来源。
//
// 整次传输共用一个 Reporter，因此速率是整次传输的平均值，不会因为换文件而重置计时起点。
// 同一传输的上报必须串行（拷贝循环天然满足），内部不加锁。
type Tracker struct {
	reporter   *Reporter
	transferID string
	filesTotal int
	bytesTotal int64

	filesDone int
	bytesDone int64 // 已完成文件的累计字节，不含当前文件
}

// NewTracker 创建传输跟踪器。filesTotal/bytesTotal 是整次传输的总量。
func NewTracker(transferID string, filesTotal int, bytesTotal int64, onProgress func(Progress)) *Tracker {
	return newTracker(transferID, filesTotal, bytesTotal, onProgress, time.Now)
}

// newTracker 允许注入时钟，便于在测试里确定性地验证节流与测速。
func newTracker(transferID string, filesTotal int, bytesTotal int64, onProgress func(Progress), now func() time.Time) *Tracker {
	return &Tracker{
		reporter:   newReporter(onProgress, now),
		transferID: transferID,
		filesTotal: filesTotal,
		bytesTotal: bytesTotal,
	}
}

// FileDone 把一个已传完文件的字节数计入累计量。
func (t *Tracker) FileDone(size int64) {
	t.filesDone++
	t.bytesDone += size
}

func (t *Tracker) report(currentFile string, currentFileBytes int64) {
	t.reporter.Report(Progress{
		TransferID:     t.transferID,
		Status:         StatusProgress,
		CurrentFile:    currentFile,
		FilesCompleted: t.filesDone,
		FilesTotal:     t.filesTotal,
		BytesDone:      t.bytesDone + currentFileBytes,
		BytesTotal:     t.bytesTotal,
	})
}

// Reader 包裹当前文件的源 reader：在 sink 拥有读循环（minio PutObject、
// sftp.File.ReadFrom）的流式上传里，于源侧观测进度；ctx 取消即中断读取。
// size 是当前文件的大小，经 Size() 暴露给 pkg/sftp 的 ReadFrom 以启用并发写。
func (t *Tracker) Reader(ctx context.Context, r io.Reader, currentFile string, size int64) *TrackedReader {
	return &TrackedReader{ctx: ctx, r: r, tracker: t, currentFile: currentFile, size: size}
}

// Writer 包裹当前文件的目标 writer，用于 source 拥有写循环的流式下载
// （sftp.File.WriteTo）；ctx 取消即中断写入。
func (t *Tracker) Writer(ctx context.Context, w io.Writer, currentFile string) *TrackedWriter {
	return &TrackedWriter{ctx: ctx, w: w, tracker: t, currentFile: currentFile}
}

// TrackedReader 是 Tracker 包裹出的源 reader。
type TrackedReader struct {
	ctx         context.Context
	r           io.Reader
	tracker     *Tracker
	currentFile string
	size        int64
	done        int64
}

// Size 返回当前文件的大小。pkg/sftp 的 ReadFrom 依赖它决定并发写窗口，
// 返回整次传输总量会让它按错误的长度切分。
func (r *TrackedReader) Size() int64 { return r.size }

func (r *TrackedReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(b)
	if n > 0 {
		r.done += int64(n)
		r.tracker.report(r.currentFile, r.done)
	}
	return n, err
}

// TrackedWriter 是 Tracker 包裹出的目标 writer。
type TrackedWriter struct {
	ctx         context.Context
	w           io.Writer
	tracker     *Tracker
	currentFile string
	done        int64
}

func (w *TrackedWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	if n > 0 {
		w.done += int64(n)
		w.tracker.report(w.currentFile, w.done)
	}
	return n, err
}

// NewProgressReader 是单文件传输的快捷方式，等价于一个 filesTotal=1 的 Tracker。
func NewProgressReader(ctx context.Context, transferID, currentFile string, r io.Reader, total int64, onProgress func(Progress)) *TrackedReader {
	return NewTracker(transferID, 1, total, onProgress).Reader(ctx, r, currentFile, total)
}

// Copy 以 32KiB 分片把 src 流式写入 dst,经独立 Reporter(100ms 节流)上报进度,
// ctx 取消即中断。用于两端都不提供 ReadFrom/WriteTo 的传输源。
func Copy(ctx context.Context, transferID string, dst io.Writer, src io.Reader, totalBytes int64, currentFile string, onProgress func(Progress)) error {
	buf := make([]byte, 32*1024)
	var bytesDone int64
	reporter := NewReporter(onProgress)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			bytesDone += int64(n)
			reporter.Report(Progress{
				TransferID:  transferID,
				Status:      StatusProgress,
				CurrentFile: currentFile,
				FilesTotal:  1,
				BytesDone:   bytesDone,
				BytesTotal:  totalBytes,
			})
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
