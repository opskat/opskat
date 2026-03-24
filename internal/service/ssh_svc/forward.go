package ssh_svc

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

// PortForwardConfig 端口转发配置
type PortForwardConfig struct {
	Type       string // "local" | "remote"
	LocalHost  string
	LocalPort  int
	RemoteHost string
	RemotePort int
}

// PortForwardInfo 端口转发信息（返回给前端）
type PortForwardInfo struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	LocalHost  string `json:"localHost"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
	Error      string `json:"error,omitempty"`
}

// portForward 端口转发（含活跃和失败的）
type portForward struct {
	info   PortForwardInfo
	cancel context.CancelFunc // nil if failed
	shared *sharedClient
}

var fwdCounter atomic.Int64

// AddPortForward 在指定会话的 SSH 连接上启动端口转发
// 即使启动失败也会记录（带 Error 字段），方便前端展示
func (m *Manager) AddPortForward(sessionID string, cfg PortForwardConfig) *PortForwardInfo {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return nil
	}
	if sess.IsClosed() {
		return nil
	}

	id := fmt.Sprintf("fwd-%d", fwdCounter.Add(1))

	fw := &portForward{
		info: PortForwardInfo{
			ID:         id,
			Type:       cfg.Type,
			LocalHost:  cfg.LocalHost,
			LocalPort:  cfg.LocalPort,
			RemoteHost: cfg.RemoteHost,
			RemotePort: cfg.RemotePort,
		},
		shared: sess.shared,
	}

	var startErr error
	switch cfg.Type {
	case "local":
		startErr = m.startLocalForward(fw, sess.shared.client)
	case "remote":
		startErr = m.startRemoteForward(fw, sess.shared.client)
	default:
		startErr = fmt.Errorf("unsupported forward type: %s", cfg.Type)
	}

	if startErr != nil {
		fw.info.Error = startErr.Error()
		log.Printf("[PortForward] %s failed: %s", id, startErr)
	}

	m.portForwards.Store(id, fw)
	return &fw.info
}

// startLocalForward 本地端口转发: 监听本地端口，通过 SSH 隧道转发到远程
func (m *Manager) startLocalForward(fw *portForward, client *ssh.Client) error {
	addr := fmt.Sprintf("%s:%d", fw.info.LocalHost, fw.info.LocalPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fw.cancel = cancel

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// listener 关闭（正常取消或 SSH 断开），标记错误
				if ctx.Err() == nil {
					fw.info.Error = "listener closed unexpectedly"
				}
				return
			}
			go func() {
				remote := fmt.Sprintf("%s:%d", fw.info.RemoteHost, fw.info.RemotePort)
				rconn, err := client.Dial("tcp", remote)
				if err != nil {
					_ = conn.Close()
					return
				}
				pipeConns(conn, rconn)
			}()
		}
	}()

	return nil
}

// startRemoteForward 远程端口转发: 监听远程端口，转发到本地
func (m *Manager) startRemoteForward(fw *portForward, client *ssh.Client) error {
	addr := fmt.Sprintf("%s:%d", fw.info.RemoteHost, fw.info.RemotePort)
	listener, err := client.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("remote listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fw.cancel = cancel

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() == nil {
					fw.info.Error = "remote listener closed unexpectedly"
				}
				return
			}
			go func() {
				local := net.JoinHostPort(fw.info.LocalHost, fmt.Sprintf("%d", fw.info.LocalPort))
				lconn, err := net.Dial("tcp", local)
				if err != nil {
					_ = conn.Close()
					return
				}
				pipeConns(conn, lconn)
			}()
		}
	}()

	return nil
}

func pipeConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// RemovePortForward 停止并移除端口转发
func (m *Manager) RemovePortForward(id string) {
	v, ok := m.portForwards.LoadAndDelete(id)
	if !ok {
		return
	}
	fw := v.(*portForward)
	if fw.cancel != nil {
		fw.cancel()
	}
}

// ListPortForwards 列出与指定会话同一 SSH 连接上的所有端口转发
func (m *Manager) ListPortForwards(sessionID string) []PortForwardInfo {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return nil
	}
	var result []PortForwardInfo
	m.portForwards.Range(func(_, value any) bool {
		fw := value.(*portForward)
		if fw.shared == sess.shared {
			result = append(result, fw.info)
		}
		return true
	})
	return result
}

// cleanupForwards 清理与指定 sharedClient 关联的所有端口转发
func (m *Manager) cleanupForwards(shared *sharedClient) {
	m.portForwards.Range(func(key, value any) bool {
		fw := value.(*portForward)
		if fw.shared == shared {
			if fw.cancel != nil {
				fw.cancel()
			}
			m.portForwards.Delete(key)
		}
		return true
	})
}
