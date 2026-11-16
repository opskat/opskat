// Package k8stest 提供测试用的假 K8s exec 端点：在 httptest 服务上接受 SPDY 升级，
// 按 kubelet 的 v4 协议收发 stdin / stdout / error / resize 流，让 exec 会话的测试走真实的 client-go 执行器。
package k8stest

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	remotecommandconsts "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
	"k8s.io/streaming/pkg/httpstream/spdy"
)

// TerminalSize 是 resize 流上的一条尺寸消息。
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// ExecSession 是一次被接受的 exec：Stdin / Stdout 已就绪，Resizes 收到客户端发来的尺寸。
type ExecSession struct {
	Request *http.Request
	Stdin   io.Reader
	Stdout  io.Writer
	Resizes <-chan TerminalSize
}

// ExecFunc 运行一次会话并返回退出码；0 = 正常退出。
type ExecFunc func(s *ExecSession) int

// EchoUntilExit 把 stdin 按行回显到 stdout，读到 "exit" 一行时以 0 退出。
func EchoUntilExit(s *ExecSession) int {
	scanner := bufio.NewScanner(s.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "exit" {
			return 0
		}
		_, _ = fmt.Fprintf(s.Stdout, "%s\n", line)
	}
	return 0
}

// ExecServer 是假 exec 端点。
type ExecServer struct {
	Server *httptest.Server

	mu       sync.Mutex
	requests []*http.Request
	run      ExecFunc
	ended    chan struct{}
}

// NewExecServer 启动假 exec 端点（TLS 时用 httptest 的自签证书）；run 为 nil 时使用 EchoUntilExit。
func NewExecServer(t *testing.T, tls bool, run ExecFunc) *ExecServer {
	if run == nil {
		run = EchoUntilExit
	}
	s := &ExecServer{run: run, ended: make(chan struct{}, 16)}
	handler := http.HandlerFunc(s.serve)
	if tls {
		s.Server = httptest.NewTLSServer(handler)
	} else {
		s.Server = httptest.NewServer(handler)
	}
	t.Cleanup(s.Server.Close)
	return s
}

// Host 是服务实际监听的 host:port，供测试拨号器把请求转到这里。
func (s *ExecServer) Host() string {
	u, _ := url.Parse(s.Server.URL)
	return u.Host
}

// CAPEM 是 TLS 服务自签证书的 PEM，用作 kubeconfig 的 CA。
func (s *ExecServer) CAPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Server.Certificate().Raw})
}

// Requests 返回收到的 exec 请求（升级前的原始请求）。
func (s *ExecServer) Requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.requests...)
}

// WaitEnded 等一次会话在服务端结束（命令返回或客户端断开），超时返回 false。
func (s *ExecServer) WaitEnded(timeout time.Duration) bool {
	select {
	case <-s.ended:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *ExecServer) serve(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, req.Clone(req.Context()))
	s.mu.Unlock()
	defer func() { s.ended <- struct{}{} }()

	if _, err := httpstream.Handshake(req, w, []string{remotecommandconsts.StreamProtocolV4Name}); err != nil {
		return
	}
	var (
		mu      sync.Mutex
		streams = map[string]httpstream.Stream{}
		arrived = make(chan struct{}, 8)
	)
	resizes := make(chan TerminalSize, 16)
	conn := spdy.NewResponseUpgrader().UpgradeResponse(w, req, func(stream httpstream.Stream, _ <-chan struct{}) error {
		kind := stream.Headers().Get(corev1.StreamType)
		if kind == corev1.StreamTypeResize {
			go decodeResizes(stream, resizes)
		}
		mu.Lock()
		streams[kind] = stream
		mu.Unlock()
		arrived <- struct{}{}
		return nil
	})
	if conn == nil {
		return
	}
	defer func() { _ = conn.Close() }()

	expected := []string{corev1.StreamTypeError, corev1.StreamTypeStdin, corev1.StreamTypeStdout}
	if req.URL.Query().Get("tty") == "true" {
		expected = append(expected, corev1.StreamTypeResize)
	}
	ready := func() bool {
		mu.Lock()
		defer mu.Unlock()
		// 与 kubelet 一样先等齐客户端要建的流（TTY 会话含 resize 流）再运行命令。
		for _, kind := range expected {
			if streams[kind] == nil {
				return false
			}
		}
		return true
	}
	for !ready() {
		select {
		case <-arrived:
		case <-conn.CloseChan():
			return
		case <-time.After(5 * time.Second):
			return
		}
	}

	done := make(chan int, 1)
	go func() {
		done <- s.run(&ExecSession{
			Request: req,
			Stdin:   streams[corev1.StreamTypeStdin],
			Stdout:  streams[corev1.StreamTypeStdout],
			Resizes: resizes,
		})
	}()
	select {
	case code := <-done:
		writeExitStatus(streams[corev1.StreamTypeError], code)
		_ = streams[corev1.StreamTypeStdout].Close()
	case <-conn.CloseChan():
	}
}

func decodeResizes(stream io.Reader, out chan<- TerminalSize) {
	dec := json.NewDecoder(stream)
	for {
		var size TerminalSize
		if err := dec.Decode(&size); err != nil {
			return
		}
		out <- size
	}
}

// writeExitStatus 按 v4 协议在 error 流上写退出状态：0 为成功，否则带退出码的 NonZeroExitCode。
func writeExitStatus(stream io.WriteCloser, code int) {
	status := metav1.Status{Status: metav1.StatusSuccess}
	if code != 0 {
		status = metav1.Status{
			Status:  metav1.StatusFailure,
			Reason:  remotecommandconsts.NonZeroExitCodeReason,
			Message: fmt.Sprintf("command terminated with non-zero exit code: %d", code),
			Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{
				Type: remotecommandconsts.ExitCodeCauseType, Message: strconv.Itoa(code),
			}}},
		}
	}
	data, _ := json.Marshal(status)
	_, _ = stream.Write(data)
	_ = stream.Close()
}

// Kubeconfig 生成指向 server 的 kubeconfig；caPEM 非空时写入 certificate-authority-data 与 tls-server-name。
func Kubeconfig(server string, caPEM []byte, tlsServerName string) string {
	cluster := fmt.Sprintf("    server: %s\n", server)
	if len(caPEM) > 0 {
		cluster += fmt.Sprintf("    certificate-authority-data: %s\n", base64.StdEncoding.EncodeToString(caPEM))
	}
	if tlsServerName != "" {
		cluster += fmt.Sprintf("    tls-server-name: %s\n", tlsServerName)
	}
	return `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
` + cluster + `contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test-token
`
}
