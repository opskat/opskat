// Package transfer 提供文件传输的通用进度原语：进度事件结构、唯一传输 ID 生成、
// 以及"节流 + 测速"的进度上报器。SFTP、ZMODEM(lrzsz) 等所有文件传输共用一套，
// 避免各自重复实现节流/测速逻辑，也让前端订阅同一份进度事件形状。
package transfer

import (
	"fmt"
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
