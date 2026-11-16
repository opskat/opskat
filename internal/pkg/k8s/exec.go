package k8s

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	utilexec "k8s.io/client-go/util/exec"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"
)

// TerminalSize 是终端的列数（Width）与行数（Height）。
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// ExecRequest 描述一次交互式（TTY）exec：Stdin 送进容器，容器输出（TTY 下 stderr 已并入）写到 Stdout，
// Sizes 上的每个尺寸同步给容器；OnConnected 在 SPDY 升级成功后调用一次。
type ExecRequest struct {
	Namespace   string
	Pod         string
	Container   string
	Command     []string
	Stdin       io.Reader
	Stdout      io.Writer
	Sizes       <-chan TerminalSize
	OnConnected func()
}

// Exec 在容器里运行命令直到它退出、连接断开或 ctx 取消；命令以非 0 退出时返回的错误可用 ExitCode 取出退出码。
func Exec(ctx context.Context, kubeconfig string, req ExecRequest, opts ...ClientOption) error {
	// exec 会话是长连接，不能套用读取类请求的 30 秒超时。
	config, err := restConfig(kubeconfig, append(append([]ClientOption{}, opts...), WithTimeout(0))...)
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create k8s clientset: %w", err)
	}
	execURL := clientset.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(req.Namespace).Name(req.Pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: req.Container,
			Command:   req.Command,
			Stdin:     true,
			Stdout:    true,
			TTY:       true,
		}, scheme.ParameterCodec).URL()

	statusSeen := &atomic.Bool{}
	transport, upgrader, err := spdyTransport(config, req.OnConnected, statusSeen)
	if err != nil {
		return fmt.Errorf("create exec transport: %w", err)
	}
	executor, err := remotecommand.NewSPDYExecutorForTransports(transport, upgrader, http.MethodPost, execURL)
	if err != nil {
		return fmt.Errorf("create exec executor: %w", err)
	}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             req.Stdin,
		Stdout:            req.Stdout,
		Tty:               true,
		TerminalSizeQueue: &sizeQueue{ctx: ctx, sizes: req.Sizes},
	})
	// 命令正常结束时服务端会在 error 流上写一条 Success 状态；连接被掐断时 error 流直接 EOF，
	// client-go 把两者都当成 nil，所以靠有没有收到状态来区分。
	if err == nil && !statusSeen.Load() {
		return ErrConnectionClosed
	}
	return err
}

// ErrConnectionClosed 表示连接在命令结束前被对端或网络断开（没有收到命令的退出状态）。
var ErrConnectionClosed = errors.New("connection closed before the command finished")

// spdyTransport 构造 exec 用的 SPDY 传输（等同 client-go 的 spdy.RoundTripperFor，另加两点）：
//   - 配置了拨号器（SSH 隧道 / 代理链 / .local）时，升级连接经它拨出。client-go 默认的 SPDY 升级连接自己拨号、
//     不看 config.Dial，会绕过资产的隧道与代理；WebSocket 执行器同样忽略 config.Dial，所以 exec 只走 SPDY。
//   - onConnected 非空时在 SPDY 升级成功后调用一次。
func spdyTransport(config *rest.Config, onConnected func(), statusSeen *atomic.Bool) (http.RoundTripper, spdy.Upgrader, error) {
	tlsConfig, err := rest.TLSConfigFor(config)
	if err != nil {
		return nil, nil, err
	}
	rtConfig := streamspdy.RoundTripperConfig{PingPeriod: 5 * time.Second}
	if config.Dial != nil {
		if tlsConfig == nil {
			// 没有任何 TLS 设置时 client-go 用系统根证书校验；而 UpgradeTransport 的 TLS 配置为 nil 时
			// SPDY 拨号会跳过证书校验，所以显式给一个默认配置。
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		rtConfig.UpgradeTransport = &http.Transport{DialContext: config.Dial, TLSClientConfig: tlsConfig}
	} else {
		rtConfig.TLS = tlsConfig
		rtConfig.Proxier = config.Proxy
	}
	roundTripper, err := streamspdy.NewRoundTripperWithConfig(rtConfig)
	if err != nil {
		return nil, nil, err
	}
	upgrader := &notifyingUpgrader{UpgradeRoundTripper: roundTripper, onConnected: onConnected, statusSeen: statusSeen}
	wrapper, err := rest.HTTPWrappersForConfig(config, roundTripper)
	if err != nil {
		return nil, nil, err
	}
	return wrapper, spdy.NewUpgraderForStreaming(upgrader), nil
}

// notifyingUpgrader 在 SPDY 升级成功后通知调用方（此后的失败都属于已建立的会话），
// 并盯着 error 流是否收到过命令的退出状态。
type notifyingUpgrader struct {
	streamhttp.UpgradeRoundTripper
	onConnected func()
	statusSeen  *atomic.Bool
}

func (u *notifyingUpgrader) NewConnection(resp *http.Response) (streamhttp.Connection, error) {
	conn, err := u.UpgradeRoundTripper.NewConnection(resp)
	if err != nil {
		return nil, err
	}
	if u.onConnected != nil {
		u.onConnected()
	}
	return &statusTrackingConn{Connection: conn, statusSeen: u.statusSeen}, nil
}

// statusTrackingConn 包住 error 流，记下它是否读到过数据。
type statusTrackingConn struct {
	streamhttp.Connection
	statusSeen *atomic.Bool
}

func (c *statusTrackingConn) CreateStream(headers http.Header) (streamhttp.Stream, error) {
	stream, err := c.Connection.CreateStream(headers)
	if err != nil || headers.Get(corev1.StreamType) != corev1.StreamTypeError {
		return stream, err
	}
	return &statusTrackingStream{Stream: stream, statusSeen: c.statusSeen}, nil
}

type statusTrackingStream struct {
	streamhttp.Stream
	statusSeen *atomic.Bool
}

func (s *statusTrackingStream) Read(p []byte) (int, error) {
	n, err := s.Stream.Read(p)
	if n > 0 {
		s.statusSeen.Store(true)
	}
	return n, err
}

// ExitCode 取出命令以非 0 退出时的退出码。
func ExitCode(err error) (int, bool) {
	var exitErr utilexec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitStatus(), true
	}
	return 0, false
}

// sizeQueue 把尺寸通道适配成 client-go 的 TerminalSizeQueue；会话结束（ctx 取消）时返回 nil 让它停止。
type sizeQueue struct {
	ctx   context.Context
	sizes <-chan TerminalSize
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size, ok := <-q.sizes:
		if !ok {
			return nil
		}
		return &remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
	case <-q.ctx.Done():
		return nil
	}
}
