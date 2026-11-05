// Package transfer 提供文件传输的通用进度原语：进度事件结构、唯一传输 ID 生成、
// 以及"节流 + 测速"的进度上报器。SFTP、ZMODEM(lrzsz) 等所有文件传输共用一套，
// 避免各自重复实现节流/测速逻辑，也让前端订阅同一份进度事件形状。
package transfer

import (
	"context"
	"fmt"
	"io"
	"sync"
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
//
// 目录传输会让多个文件同时在传，因此 Tracker 可被并发调用：每个文件经 File 各持一个
// TrackedFile，进度事件的 BytesDone 是"已完成文件 + 所有在传文件已传字节"，
// CurrentFile 是最近有字节流过的文件。上报回调在锁内调用，因此回调之间天然串行、按序。
type Tracker struct {
	transferID string
	filesTotal int
	bytesTotal int64

	mu        sync.Mutex
	reporter  *Reporter
	filesDone int
	bytesDone int64        // 已完成文件的累计字节
	inFlight  int64        // 尚未完成的文件已传字节之和
	serial    *TrackedFile // 经 Reader/Writer 简写打开、等待 FileDone 结清的文件
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

// File 开始跟踪一个文件，size 是它的大小。并发传多个文件时每个文件各持一个
// TrackedFile，传完调用它的 Done。
func (t *Tracker) File(currentFile string, size int64) *TrackedFile {
	return &TrackedFile{tracker: t, name: currentFile, size: size}
}

// Reader 是串行传输的简写：开始跟踪 currentFile 并包裹它的源 reader，传完用 FileDone 结清。
func (t *Tracker) Reader(ctx context.Context, r io.Reader, currentFile string, size int64) *TrackedReader {
	return t.serialFile(currentFile, size).Reader(ctx, r)
}

// Writer 是串行传输的简写：开始跟踪 currentFile 并包裹它的目标 writer，传完用 FileDone 结清。
func (t *Tracker) Writer(ctx context.Context, w io.Writer, currentFile string) *TrackedWriter {
	return t.serialFile(currentFile, 0).Writer(ctx, w)
}

func (t *Tracker) serialFile(currentFile string, size int64) *TrackedFile {
	f := t.File(currentFile, size)
	t.mu.Lock()
	t.serial = f
	t.mu.Unlock()
	return f
}

// FileDone 把一个已传完文件的字节数计入累计量，并结清经 Reader/Writer 简写打开的当前文件。
// 只适用于串行传输（同一时刻只有一个文件在传）；并发传输用 File(...).Done()。
func (t *Tracker) FileDone(size int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var streamed int64
	if t.serial != nil {
		streamed = t.serial.done
		t.serial = nil
	}
	t.completeLocked(streamed, size)
}

func (t *Tracker) completeLocked(streamed, size int64) {
	t.filesDone++
	t.bytesDone += size
	t.inFlight -= streamed
}

// TrackedFile 是一次传输里的一个文件。
type TrackedFile struct {
	tracker *Tracker
	name    string
	size    int64
	done    int64 // 已传字节，受 tracker.mu 保护
}

// Reader 包裹该文件的源 reader：在 sink 拥有读循环（minio PutObject、
// sftp.File.ReadFrom）的流式上传里，于源侧观测进度；ctx 取消即中断读取。
func (f *TrackedFile) Reader(ctx context.Context, r io.Reader) *TrackedReader {
	return &TrackedReader{ctx: ctx, r: r, file: f}
}

// Writer 包裹该文件的目标 writer，用于 source 拥有写循环的流式下载
// （sftp.File.WriteTo）；ctx 取消即中断写入。
func (f *TrackedFile) Writer(ctx context.Context, w io.Writer) *TrackedWriter {
	return &TrackedWriter{ctx: ctx, w: w, file: f}
}

// Done 把该文件按其大小计入已完成量，并从在传字节里结清它已传的部分。
func (f *TrackedFile) Done() {
	t := f.tracker
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completeLocked(f.done, f.size)
}

func (f *TrackedFile) advance(n int) {
	t := f.tracker
	t.mu.Lock()
	defer t.mu.Unlock()
	f.done += int64(n)
	t.inFlight += int64(n)
	t.reporter.Report(Progress{
		TransferID:     t.transferID,
		Status:         StatusProgress,
		CurrentFile:    f.name,
		FilesCompleted: t.filesDone,
		FilesTotal:     t.filesTotal,
		BytesDone:      t.bytesDone + t.inFlight,
		BytesTotal:     t.bytesTotal,
	})
}

// TrackedReader 是 TrackedFile 包裹出的源 reader。
type TrackedReader struct {
	ctx  context.Context
	r    io.Reader
	file *TrackedFile
}

// Size 返回当前文件的大小。pkg/sftp 的 ReadFrom 依赖它决定并发写窗口，
// 返回整次传输总量会让它按错误的长度切分。
func (r *TrackedReader) Size() int64 { return r.file.size }

func (r *TrackedReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(b)
	if n > 0 {
		r.file.advance(n)
	}
	return n, err
}

// TrackedWriter 是 TrackedFile 包裹出的目标 writer。
type TrackedWriter struct {
	ctx  context.Context
	w    io.Writer
	file *TrackedFile
}

func (w *TrackedWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	if n > 0 {
		w.file.advance(n)
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
