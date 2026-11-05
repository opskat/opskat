// Package streambatch 把字节输出流的高频小块写入攒成少数几批再交给下游
// （通常是一次 Wails EventsEmit），供终端 / 日志 / 远程桌面等输出泵共用。
//
// flush 的时机：缓冲累计到 maxBytes 时在写入方同步 flush；否则在第一块数据进入
// 空缓冲时启动一个 window 计时器，到期 flush。计时器只在有待发数据时存在，flush 后
// 即停止——空闲会话不会被定时唤醒。Close 同步交付剩余数据。
package streambatch

import (
	"sync"
	"time"
)

const (
	// DefaultWindow 是终端类输出的攒批窗口：人眼无感，又能把突发输出合并成少数几次事件。
	DefaultWindow = 10 * time.Millisecond
	// DefaultMaxBytes 是单批上限：大块输出到达上限即刻 flush，不额外等窗口。
	DefaultMaxBytes = 32 * 1024
)

// afterFunc 是计时器的创建入口，测试替换它来统计计时器启动次数。
var afterFunc = time.AfterFunc

// Batcher 合并 Add 进来的字节并按批调用 flush。flush 收到的切片归调用方所有。
// flush 调用彼此串行，投递顺序与 Add 顺序一致；flush 可能在写入方 goroutine
// （到达上限 / Close）或计时器 goroutine 上执行。
type Batcher struct {
	window   time.Duration
	maxBytes int
	flush    func([]byte)

	// emitMu 串行化"取出缓冲 + 调用 flush"，保证批次按取出顺序交付。
	emitMu sync.Mutex

	mu    sync.Mutex
	buf   []byte
	timer *time.Timer // 非 nil 表示有一个计时器在等待；仅在 buf 非空时存在
	gen   uint64      // 每启动一个计时器 +1，让已被取代的计时器回调识别自己过期
}

// New 创建一个攒批器。window 为最长攒批时间，maxBytes 为单批上限。
func New(window time.Duration, maxBytes int, flush func([]byte)) *Batcher {
	return &Batcher{window: window, maxBytes: maxBytes, flush: flush}
}

// Add 追加一块数据（会复制，调用方可复用 p）。
func (b *Batcher) Add(p []byte) {
	if len(p) == 0 {
		return
	}
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	if len(b.buf) >= b.maxBytes {
		b.mu.Unlock()
		b.emit()
		return
	}
	if b.timer == nil {
		b.gen++
		gen := b.gen
		b.timer = afterFunc(b.window, func() { b.onTimer(gen) })
	}
	b.mu.Unlock()
}

// Close 同步交付剩余数据并停止计时器。输出流结束（EOF / 读错误 / 会话关闭）时调用，
// 之后不应再 Add。
func (b *Batcher) Close() {
	b.emit()
}

func (b *Batcher) emit() {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	b.mu.Lock()
	data := b.takeLocked()
	b.mu.Unlock()
	if len(data) > 0 {
		b.flush(data)
	}
}

func (b *Batcher) onTimer(gen uint64) {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	b.mu.Lock()
	if b.timer == nil || b.gen != gen {
		// 这个计时器在触发前已被上限 flush / Close 取代，缓冲归别的批次处理。
		b.mu.Unlock()
		return
	}
	data := b.takeLocked()
	b.mu.Unlock()
	b.flush(data)
}

// takeLocked 取走缓冲并停掉计时器。调用方持有 mu。
func (b *Batcher) takeLocked() []byte {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	data := b.buf
	b.buf = nil
	return data
}

func (b *Batcher) timerPending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.timer != nil
}
