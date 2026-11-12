package ssh_svc

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// silentListener 接受 TCP 但从不发送任何字节：SSH 握手在等 banner 时永远阻塞。
func silentListener(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

// holdChannelServer 是完成 SSH 握手、但对 direct-tcpip 通道请求永不应答的跳板机：
// 经它连接下一跳时拨号一直等待（模拟跳板机到不可达目标的长时间连接超时）。
func holdChannelServer(t *testing.T) (string, int) {
	t.Helper()
	cfg := &ssh.ServerConfig{PasswordCallback: acceptAnyPassword}
	cfg.AddHostKey(newTestHostKey(t))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sc.Close() }()
				go ssh.DiscardRequests(reqs)
				var pending []ssh.NewChannel
				for nc := range chans {
					pending = append(pending, nc) // 不 Accept 也不 Reject
				}
				_ = pending
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func acceptAnyPassword(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }

func passwordJump(host string, port int) JumpHostEntry {
	return JumpHostEntry{Host: host, Port: port, Username: "jump", AuthType: "password", Password: "p"}
}

// dialCanceledAfter 在 ctx 于 cancelAfter 后取消的情况下拨号，返回耗时与错误；超过 limit 仍未返回则失败。
func dialCanceledAfter(t *testing.T, cfg ConnectConfig, cancelAfter, limit time.Duration) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(cancelAfter, cancel)
	cfg.Ctx = ctx

	type result struct {
		client *ssh.Client
		err    error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		c, _, err := NewManager().Dial(cfg)
		done <- result{c, err}
	}()
	select {
	case r := <-done:
		if r.client != nil {
			_ = r.client.Close()
		}
		return time.Since(start), r.err
	case <-time.After(limit):
		t.Fatalf("Dial still blocked %v after ctx was canceled at %v", limit, cancelAfter)
		return 0, nil
	}
}

func TestDialCanceledDuringHandshakeReturnsPromptly(t *testing.T) {
	host, port := silentListener(t)
	elapsed, err := dialCanceledAfter(t, ConnectConfig{
		Host: host, Port: port, Username: "u", AuthType: "password", Password: "p",
	}, 200*time.Millisecond, 3*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > time.Second {
		t.Fatalf("returned after %v, want shortly after the 200ms cancel", elapsed)
	}
}

func TestDialViaJumpCanceledWhileOpeningNextHopReturnsPromptly(t *testing.T) {
	jumpHost, jumpPort := holdChannelServer(t)
	_, err := dialCanceledAfter(t, ConnectConfig{
		Host: "10.255.255.1", Port: 22, Username: "u", AuthType: "password", Password: "p",
		JumpHosts: []JumpHostEntry{passwordJump(jumpHost, jumpPort)},
	}, 500*time.Millisecond, 3*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDialViaJumpCanceledDuringTargetHandshakeReturnsPromptly(t *testing.T) {
	jump := newJumpForwardServer(t, &ssh.ServerConfig{PasswordCallback: acceptAnyPassword})
	jumpHost, jumpPort := jump.hostPort()
	targetHost, targetPort := silentListener(t)
	_, err := dialCanceledAfter(t, ConnectConfig{
		Host: targetHost, Port: targetPort, Username: "u", AuthType: "password", Password: "p",
		JumpHosts: []JumpHostEntry{passwordJump(jumpHost, jumpPort)},
	}, 500*time.Millisecond, 3*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// 握手成功后才取消 ctx（例如一轮 AI 对话结束），已建立的连接不能被关掉。
func TestDialedClientSurvivesLaterCancel(t *testing.T) {
	server := newJumpForwardServer(t, &ssh.ServerConfig{PasswordCallback: acceptAnyPassword})
	host, port := server.hostPort()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	client, closers, err := NewManager().Dial(ConnectConfig{
		Host: host, Port: port, Username: "u", AuthType: "password", Password: "p", Ctx: ctx,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		for _, c := range closers {
			_ = c.Close()
		}
	})
	cancel()
	time.Sleep(100 * time.Millisecond)

	conn, err := client.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(echo.Addr().(*net.TCPAddr).Port)))
	if err != nil {
		t.Fatalf("client unusable after ctx cancel: %v", err)
	}
	_ = conn.Close()
}
