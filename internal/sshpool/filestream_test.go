package sshpool

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func framed(t *testing.T, frames ...[]byte) *bufio.Reader {
	t.Helper()
	var buf bytes.Buffer
	for _, payload := range frames {
		if err := WriteFrame(&buf, FrameFileData, payload); err != nil {
			t.Fatalf("write data frame: %v", err)
		}
	}
	if err := WriteFrame(&buf, FrameFileEOF, nil); err != nil {
		t.Fatalf("write eof frame: %v", err)
	}
	return bufio.NewReader(&buf)
}

// 上传的帧序列要还原成一条完整字节流：服务端拿它一次性灌进远端文件，
// 而不是逐帧 Write —— 后者每 32KB 就要等一个 SFTP 回包。
func TestFileFrameReaderStreamsUntilEOF(t *testing.T) {
	r := &fileFrameReader{reader: framed(t, []byte("hello "), []byte("world"))}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

// 连接中途断掉时绝不能当成"传完了"：那会让一个被截断的文件以 FrameOK 收场。
func TestFileFrameReaderRejectsTruncatedStream(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameFileData, []byte("hello")); err != nil {
		t.Fatalf("write data frame: %v", err)
	}
	r := &fileFrameReader{reader: bufio.NewReader(&buf)}

	_, err := io.ReadAll(r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAll error = %v, want io.ErrUnexpectedEOF", err)
	}
}

// 上传连接上出现的其他帧类型是协议错误，同样不能被读成正常结束。
func TestFileFrameReaderRejectsUnexpectedFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameStdin, []byte("oops")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	r := &fileFrameReader{reader: bufio.NewReader(&buf)}

	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("expected an error for an unexpected frame type on the upload stream")
	}
}

// 下载写入端要按帧协议的上限切分：WriteTo 交下来的块大小由 pkg/sftp 决定，
// 超过 MaxFramePayload 的一次写入必须自己拆开，而不是让 WriteFrame 报错。
func TestFileFrameWriterSplitsOversizedWrites(t *testing.T) {
	var conn bytes.Buffer
	body := []byte(strings.Repeat("x", MaxFramePayload*2+7))

	n, err := fileFrameWriter{conn: &conn}.Write(body)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(body) {
		t.Fatalf("wrote %d bytes, want %d", n, len(body))
	}

	reader := bufio.NewReader(&conn)
	var got []byte
	for frames := 0; ; frames++ {
		if frames > 8 {
			t.Fatal("帧数异常，写入端没有按上限切分")
		}
		frameType, payload, err := ReadFrame(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if frameType != FrameFileData {
			t.Fatalf("frame type = 0x%02x, want FrameFileData", frameType)
		}
		got = append(got, payload...)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("帧还原出 %d 字节，want %d", len(got), len(body))
	}
}
