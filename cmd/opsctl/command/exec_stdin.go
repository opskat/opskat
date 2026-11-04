package command

import (
	"bytes"
	"io"
	"os"
	"time"
)

// stdinPeekWait 是判断管道里有没有内容时最多等多久。上游已经写了或者已经关闭时立刻就有
// 结论；等这么久还没有结论的按有输入处理（见 inspectStdin）。
const stdinPeekWait = 200 * time.Millisecond

// inspectStdin 判断 stdin 有没有要转发给远端命令的内容，返回转发用的 reader（没有内容时
// 为 nil）和判断结果：
//   - 终端、空设备（< /dev/null）：没有；
//   - 重定向的普通文件：按大小判断；
//   - 管道：先读第一块。读到内容算有；立刻读到 EOF（调用方关掉了 stdin）算没有。等了 wait
//     还没有结论的（调用方开着管道却不写，或者上游还没开始输出）按有处理：之后才写进来的
//     内容同样会被转发，不能让它绕过模型审核。wait 为 0 表示不预读（资产没开模型审核，
//     用不到这个判断），管道直接转发，和没有模型审核时一样。
//
// 预读出来的内容拼回 reader 的开头，转发时一个字节不少。
func inspectStdin(f *os.File, wait time.Duration) (io.Reader, bool) {
	stat, err := f.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice != 0 {
		return nil, false
	}
	if stat.Mode().IsRegular() {
		if stat.Size() == 0 {
			return nil, false
		}
		return f, true
	}
	if wait == 0 {
		return f, true
	}

	first := make(chan stdinChunk, 1)
	go func() {
		buf := make([]byte, 32*1024)
		n, err := f.Read(buf)
		first <- stdinChunk{data: buf[:n], err: err}
	}()
	select {
	case c := <-first:
		if len(c.data) == 0 && c.err == io.EOF {
			return nil, false
		}
		return c.reader(f), true
	case <-time.After(wait):
		return &pendingStdin{first: first, f: f}, true
	}
}

// stdinChunk 是预读到的第一块内容和那次读取的错误。
type stdinChunk struct {
	data []byte
	err  error
}

// reader 先给出预读到的内容，再接着读 f；预读时已经出错（包括 EOF）就在内容之后返回那个错误。
func (c stdinChunk) reader(f *os.File) io.Reader {
	var rest io.Reader = f
	if c.err != nil {
		rest = errReader{c.err}
	}
	return io.MultiReader(bytes.NewReader(c.data), rest)
}

// pendingStdin 是等不到结论时交给转发的 reader：第一次读时接上还在进行的那次预读，之后照常读。
type pendingStdin struct {
	first <-chan stdinChunk
	f     *os.File
	r     io.Reader
}

func (p *pendingStdin) Read(b []byte) (int, error) {
	if p.r == nil {
		p.r = (<-p.first).reader(p.f)
	}
	return p.r.Read(b)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
