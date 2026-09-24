// Package sftpio 收敛 SFTP 的数据面原语：所有"把字节搬过 SFTP 连接"的地方都从这里走。
//
// 为什么需要这一层：pkg/sftp 的吞吐取决于**调用方式**而不是连接本身。`io.Copy` 与
// `File.Write` 会退化成"发一个 32KB 包、等一个回包"，在高延迟链路上把吞吐钉死在
// 32KB/RTT（实测 9ms 链路上约 5 MB/s，而同一条链路流水线后有 30 MB/s 以上）。
// `File.ReadFrom` 能并发，但前提是客户端开了 UseConcurrentWrites **且** 源 reader 恰好
// 暴露 Size()/Stat() —— 中间隔一层计数或包装就静默退化，这正是 opsctl cp 慢下来的原因。
// 因此这里只用 ReadFromWithConcurrency / WriteTo 这两个**无条件**并发的原语，
// 让"流水线"成为调用即得的性质，而不是一串前提条件。
//
// 代价：并发写在中途失败时，目标文件可能比成功写入的部分更长（中间留空洞）。
// 每个调用方都必须在失败时删除或截断目标 —— 这是用本包的前置条件，不是可选项。
package sftpio

import (
	"io"
)

// ConcurrentWriter 是远端文件的写入面。ReadFromWithConcurrency 由 *sftp.File 提供，
// 传 0 表示用客户端的最大并发度。
type ConcurrentWriter interface {
	io.Writer
	ReadFromWithConcurrency(r io.Reader, concurrency int) (int64, error)
}

// ReadCloser / WriteCloser 是远端文件句柄在本包里的形状。把并发原语写进接口，
// 而不是运行时探测 —— 少一个方法就是一条悄悄退化成逐包往返的路径。
type ReadCloser interface {
	io.ReadCloser
	io.WriterTo
}

type WriteCloser interface {
	io.WriteCloser
	ConcurrentWriter
}

// Upload 把 src 写进远端文件 dst，写请求并发发出。
//
// 并发度交给客户端上限：pkg/sftp 按实际切出的写请求数派发，小文件只会产生一个请求，
// 不会因此多发包。
func Upload(dst ConcurrentWriter, src io.Reader) (int64, error) {
	return dst.ReadFromWithConcurrency(src, 0)
}

// Relay 远端到远端搬运：读腿用 WriteTo 的并发读，写腿用 Upload 的并发写，中间用管道接上。
//
// 不能用 io.Copy 代替：它会选中 src.WriteTo(dst)，读腿确实并发，但随后每次只把一个
// maxPacket 大小的块交给 dst.Write —— 而 File.Write 对不超过一个包的写入永远是单次往返，
// 于是写腿整条退化成串行，连 UseConcurrentWrites 都绕不过去。
func Relay(dst ConcurrentWriter, src io.WriterTo) (int64, error) {
	pr, pw := io.Pipe()
	go func() {
		_, err := src.WriteTo(pw)
		_ = pw.CloseWithError(err)
	}()
	n, err := Upload(dst, pr)
	// 写腿先出错时读腿还在往管道里写，关掉读端让它立刻收敛，否则这个协程会一直挂着。
	_ = pr.CloseWithError(err)
	return n, err
}
