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

// ProgressReader 包裹一个 io.Reader：在 sink 拥有读循环（如 minio PutObject）的流式上传里，
// 于源 reader 侧观测进度并经 Reporter 节流上报；ctx 取消即中断读取。同一传输的 Read 串行，
// 内部无需加锁（与 Reporter 约定一致）。
type ProgressReader struct {
	ctx         context.Context
	r           io.Reader
	reporter    *Reporter
	transferID  string
	currentFile string
	total       int64
	done        int64
}

// NewProgressReader 构造进度 reader，内部持有独立 Reporter（100ms 节流）。
func NewProgressReader(ctx context.Context, transferID, currentFile string, r io.Reader, total int64, onProgress func(Progress)) *ProgressReader {
	return &ProgressReader{
		ctx:         ctx,
		r:           r,
		reporter:    NewReporter(onProgress),
		transferID:  transferID,
		currentFile: currentFile,
		total:       total,
	}
}

func (p *ProgressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.reporter.Report(Progress{
			TransferID:  p.transferID,
			Status:      StatusProgress,
			CurrentFile: p.currentFile,
			FilesTotal:  1,
			BytesDone:   p.done,
			BytesTotal:  p.total,
		})
	}
	return n, err
}

// Copy 以 32KiB 分片把 src 流式写入 dst,经独立 Reporter(100ms 节流)上报进度,
// ctx 取消即中断。镜像 sftp_svc.copyWithProgress,让每种传输源共用一套节流拷贝循环。
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
