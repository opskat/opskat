// Package sftpiotest 提供"SFTP 请求有没有流水线"的进程内验证手段：一个真实的 SFTP
// 服务端（net.Pipe + sftp.NewServer，服务真实文件系统），外加一条装在客户端连接上的
// 人造延迟。
//
// 为什么需要人造延迟：高延迟链路上"每 32KB 一问一答"与"多个请求同时在途"的吞吐差一个
// 数量级，但在本地回环上两者都快，计时区分不出来，也没法在 CI 上稳定断言。给每个回包
// 加一段固定延迟后，"同时在途的请求数"就成了确定的观测量 —— 一问一答的客户端永远只能
// 是 1，流水线的客户端会立刻堆到并发上限。
package sftpiotest

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// SFTP 协议的请求类型（draft-ietf-secsh-filexfer 第 3 节），用于识别线上的包。
const (
	PacketRead  byte = 5
	PacketWrite byte = 6
)

// responseLatency 是每个回包被交给客户端前的等待时间，模拟链路往返。
// 取值只需远大于本机调度抖动，同时让整条用例仍在几十毫秒量级。
const responseLatency = 5 * time.Millisecond

// Conn 是装在客户端连接上的观测点，记录目标类型请求的最大同时在途数。
type Conn struct {
	conn net.Conn
	typ  byte

	mu        sync.Mutex
	requests  packetScanner
	responses packetScanner
	inFlight  int
	max       int
}

// MaxInFlight 返回整条连接上同时在途的目标类型请求数的峰值。
func (c *Conn) MaxInFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

func (c *Conn) Write(p []byte) (int, error) {
	n, err := c.conn.Write(p)
	if n > 0 {
		c.sent(c.requests.count(p[:n], c.typ))
	}
	return n, err
}

func (c *Conn) Read(p []byte) (int, error) {
	time.Sleep(responseLatency)
	n, err := c.conn.Read(p)
	if n > 0 {
		c.received(c.responses.countAll(p[:n]))
	}
	return n, err
}

func (c *Conn) Close() error { return c.conn.Close() }

func (c *Conn) sent(n int) {
	if n == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlight += n
	c.max = max(c.max, c.inFlight)
}

// received 只在还有在途请求时才扣减：握手、open、stat 这些非目标类型的往返也会带回包，
// 不排除掉它们，在途计数会被扣成负数。
func (c *Conn) received(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlight = max(0, c.inFlight-n)
}

// packetScanner 在字节流上数出完整的 SFTP 包：uint32 长度 + 包体，包体首字节是类型。
// 一次 Read/Write 未必正好对应一个包，因此必须按流增量解析而不是按调用计数。
type packetScanner struct {
	header  []byte // 未凑满 4 字节的长度前缀
	remain  uint32 // 当前包剩余待跳过的字节数
	typeGot bool   // 当前包的类型字节是否已读到
}

// count 数出 b 里类型为 want 的包数。
func (s *packetScanner) count(b []byte, want byte) int {
	return s.scan(b, want, false)
}

// countAll 数出 b 里的全部包数。
func (s *packetScanner) countAll(b []byte) int {
	return s.scan(b, 0, true)
}

func (s *packetScanner) scan(b []byte, want byte, all bool) int {
	matched := 0
	for len(b) > 0 {
		if s.remain == 0 {
			need := 4 - len(s.header)
			if len(b) < need {
				s.header = append(s.header, b...)
				return matched
			}
			s.header = append(s.header, b[:need]...)
			s.remain = binary.BigEndian.Uint32(s.header)
			s.header = s.header[:0]
			s.typeGot = false
			b = b[need:]
			continue
		}
		if !s.typeGot {
			s.typeGot = true
			if all || b[0] == want {
				matched++
			}
		}
		n := min(uint32(len(b)), s.remain)
		s.remain -= n
		b = b[n:]
	}
	return matched
}

// NewClient 起一个根在 root 的进程内 SFTP 服务端，返回连到它的客户端与观测点。
// 客户端就是生产代码用的 pkg/sftp 客户端，因此被验证的是真实的请求发送方式。
func NewClient(t *testing.T, root string, packetType byte) (*sftp.Client, *Conn) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(root))
	if err != nil {
		t.Fatalf("new sftp server: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve()
	}()

	observed := &Conn{conn: clientConn, typ: packetType}
	// 读写两端都是同一个连接：客户端 Close 关的就是它，否则 recv 协程会卡在 net.Pipe 上。
	client, err := sftp.NewClientPipe(observed, observed)
	if err != nil {
		t.Fatalf("new sftp client: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-served
	})
	return client, observed
}

// NewPlainClient 起一个不加延迟、不做观测的客户端。
// 一次搬运里只有被观测的那条腿该被拖慢：另一条腿也带延迟的话，它会成为瓶颈，
// 把被测腿喂成"一次一个请求"，串行退化就被掩盖了。
func NewPlainClient(t *testing.T, root string) *sftp.Client {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(root))
	if err != nil {
		t.Fatalf("new sftp server: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve()
	}()

	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatalf("new sftp client: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-served
	})
	return client
}
