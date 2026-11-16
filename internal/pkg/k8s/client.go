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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/opskat/opskat/internal/pkg/netdial"
)

type ClientOption func(*clientOptions)

type clientOptions struct {
	dial        func(ctx context.Context, network, address string) (net.Conn, error)
	contextName string
	timeout     *time.Duration
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

func WithTimeout(timeout time.Duration) ClientOption {
	return func(opts *clientOptions) {
		opts.timeout = &timeout
	}
}

func buildClient(kubeconfig string, opts ...ClientOption) (*kubernetes.Clientset, error) {
	config, err := restConfig(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s clientset: %w", err)
	}
	return clientset, nil
}

// restConfig 由 kubeconfig 与选项构造 rest.Config；typed、discovery 与原始 REST 客户端都从这里取配置，
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
	if clientOpts.timeout != nil {
		config.Timeout = *clientOpts.timeout
	} else {
		config.Timeout = 30 * time.Second
	}
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

func StreamPodLogs(ctx context.Context, kubeconfig, namespace, podName, container string, tailLines int64, opts ...ClientOption) (io.ReadCloser, error) {
	streamOpts := append(append([]ClientOption{}, opts...), WithTimeout(0))
	clientset, err := buildClient(kubeconfig, streamOpts...)
	if err != nil {
		return nil, err
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
