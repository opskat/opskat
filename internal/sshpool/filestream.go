package sshpool

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// fileFrameReader 把上传连接上的帧序列还原成一条连续字节流。
//
// 服务端据此用一个并发写流水线把整个文件灌进远端，而不是收一帧写一帧 ——
// 逐帧写每 32KB 就要等一个 SFTP 回包，高延迟链路上整条上传被钉死在 32KB/RTT。
type fileFrameReader struct {
	reader *bufio.Reader
	buf    []byte
	eof    bool
}

func (r *fileFrameReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		frameType, payload, err := ReadFrame(r.reader)
		if err != nil {
			// 流在帧边界上断掉时 ReadFrame 交出 io.EOF。原样上抛就等于宣称"文件读完了"，
			// 半个文件会被当成上传成功 —— 只有 FrameFileEOF 才算读完。
			if errors.Is(err, io.EOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		switch frameType {
		case FrameFileData:
			r.buf = payload
		case FrameFileEOF:
			r.eof = true
			return 0, io.EOF
		default:
			return 0, fmt.Errorf("unexpected frame type on upload stream: 0x%02x", frameType)
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// fileFrameWriter 把下载的字节流切成 FrameFileData 帧发回客户端。
// 一次写入的长度由上游（pkg/sftp 的 WriteTo）决定，这里按帧协议上限自行切分。
type fileFrameWriter struct {
	conn io.Writer
}

func (w fileFrameWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > MaxFramePayload {
			chunk = chunk[:MaxFramePayload]
		}
		if err := WriteFrame(w.conn, FrameFileData, chunk); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}
