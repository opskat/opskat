package k8s

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/opskat/opskat/internal/pkg/netdial"
)

type ClientOption func(*clientOptions)

type clientOptions struct {
	dial        func(ctx context.Context, network, address string) (net.Conn, error)
	contextName string
}

func WithDial(dial func(ctx context.Context, network, address string) (net.Conn, error)) ClientOption {
	return func(opts *clientOptions) {
		opts.dial = dial
	}
}

func WithContext(contextName string) ClientOption {
	return func(opts *clientOptions) {
		opts.contextName = contextName
	}
}

// maxDialedConns 是经自定义拨号器（SSH 隧道 / 代理链 / .local）到集群同时存在的 TCP 连接上限。
// API Server 走 HTTP/2，一条连接即可承载全部并发请求；不设上限时，连接建立前同时发出的请求各拨一次，
// 经 SSH 跳板就是各握手一次，几十个请求会撞上跳板机的并发握手限制（sshd MaxStartups）。
const maxDialedConns = 4

// Conn 是到一个集群的可复用连接：配置只解析一次，读写请求共用同一个 HTTP 客户端，
// 经 HTTP/2 多路复用或 keep-alive 复用少量 TCP 连接。并发安全；不再使用时调 Close。
// 长连接（日志流、exec）各自建立连接，但用同一份配置与拨号器。
type Conn struct {
	config *rest.Config
	client *http.Client
}

// NewConn 由 kubeconfig 与选项构造连接；不在这里拨号，第一次请求时才建立 TCP 连接。
func NewConn(kubeconfig string, opts ...ClientOption) (*Conn, error) {
	config, err := restConfig(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	httpConfig := rest.CopyConfig(config)
	if config.Dial != nil {
		httpConfig.Wrap(limitDialedConns)
	}
	client, err := rest.HTTPClientFor(httpConfig)
	if err != nil {
		return nil, fmt.Errorf("create k8s http client: %w", err)
	}
	return &Conn{config: config, client: client}, nil
}

// limitDialedConns 给经自定义拨号器的传输层设连接上限。配置了 Dial 时 client-go 不缓存传输层，
// 每个客户端拿到的是自己新建的 *http.Transport，改它不影响别的客户端。
func limitDialedConns(rt http.RoundTripper) http.RoundTripper {
	if t, ok := rt.(*http.Transport); ok {
		t.MaxConnsPerHost = maxDialedConns
	}
	return rt
}

// Close 关闭空闲连接；进行中的请求照常完成，完成后其连接随即关闭。
func (c *Conn) Close() error {
	utilnet.CloseIdleConnectionsFor(c.client.Transport)
	return nil
}

// streamConfig 是长连接用的配置副本：不套用读取类请求的 30 秒超时。
func (c *Conn) streamConfig() *rest.Config {
	config := rest.CopyConfig(c.config)
	config.Timeout = 0
	return config
}

// restConfig 由 kubeconfig 与选项构造 rest.Config；typed、discovery、dynamic 与原始 REST 客户端都从这里取配置，
// 保证 context、超时与自定义拨号（SSH 隧道 / 代理链 / .local）一致。
func restConfig(kubeconfig string, opts ...ClientOption) (*rest.Config, error) {
	clientOpts := &clientOptions{}
	for _, opt := range opts {
		opt(clientOpts)
	}

	if kubeconfig == "" {
		return nil, fmt.Errorf("kubeconfig is required")
	}
	clientCfg, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	overrides := &clientcmd.ConfigOverrides{}
	if clientOpts.contextName != "" {
		overrides.CurrentContext = clientOpts.contextName
	}
	config, err := clientcmd.NewDefaultClientConfig(*clientCfg, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config from kubeconfig: %w", err)
	}
	config.Timeout = 30 * time.Second
	if clientOpts.dial != nil {
		config.Dial = clientOpts.dial
		config.Proxy = func(*http.Request) (*url.URL, error) {
			return nil, nil
		}
	} else if netdial.IsLocalAddr(config.Host) {
		// 直连 .local 集群才换统一拨号器(参数与 client-go 默认一致,环境变量代理照常生效);
		// 设置 Dial 会让 client-go 不再缓存 transport,其余直连保持默认。
		config.Dial = netdial.Default().DialContext
	}
	return config, nil
}

func StreamPodLogs(ctx context.Context, conn *Conn, namespace, podName, container string, tailLines int64) (io.ReadCloser, error) {
	clientset, err := kubernetes.NewForConfig(conn.streamConfig())
	if err != nil {
		return nil, fmt.Errorf("create k8s clientset: %w", err)
	}

	logOpts := &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
		TailLines: &tailLines,
	}

	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("stream pod logs: %w", err)
	}
	return stream, nil
}
