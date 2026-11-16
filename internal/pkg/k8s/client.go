package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

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

type ContainerDetail struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
}

type ConditionDetail struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type EventDetail struct {
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	FirstTime string `json:"first_time"`
	LastTime  string `json:"last_time"`
	Count     int32  `json:"count"`
}

type PodDetail struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Status       string            `json:"status"`
	NodeName     string            `json:"node_name"`
	PodIP        string            `json:"pod_ip"`
	HostIP       string            `json:"host_ip"`
	CreationTime string            `json:"creation_time"`
	Age          string            `json:"age"`
	Ready        string            `json:"ready"`
	RestartCount int32             `json:"restart_count"`
	QosClass     string            `json:"qos_class"`
	Containers   []ContainerDetail `json:"containers"`
	Conditions   []ConditionDetail `json:"conditions"`
	Events       []EventDetail     `json:"events"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	YAML         string            `json:"yaml"`
}

func GetPodDetail(ctx context.Context, kubeconfig, namespace, podName string, opts ...ClientOption) (*PodDetail, error) {
	clientset, err := buildClient(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s/%s: %w", namespace, podName, err)
	}

	now := time.Now()

	readyContainers := int32(0)
	totalContainers := len(pod.Spec.Containers)
	restarts := int32(0)
	statusByName := make(map[string]*corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		statusByName[cs.Name] = cs
	}
	containers := make([]ContainerDetail, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		state := "Unknown"
		ready := false
		cr := int32(0)
		if cs, ok := statusByName[c.Name]; ok {
			ready = cs.Ready
			cr = cs.RestartCount
			restarts += cr
			if ready {
				readyContainers++
			}
			if cs.State.Running != nil {
				state = "Running"
			} else if cs.State.Waiting != nil {
				state = "Waiting: " + cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil {
				state = "Terminated: " + cs.State.Terminated.Reason
			}
		}
		containers = append(containers, ContainerDetail{
			Name:         c.Name,
			Image:        c.Image,
			State:        state,
			Ready:        ready,
			RestartCount: cr,
		})
	}

	status := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		status = pod.Status.Reason
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			status = cs.State.Waiting.Reason
		}
	}

	conditions := make([]ConditionDetail, 0, len(pod.Status.Conditions))
	for _, c := range pod.Status.Conditions {
		conditions = append(conditions, ConditionDetail{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	events, _ := getPodEvents(ctx, clientset, namespace, podName)

	jsonBytes, err := json.Marshal(&pod)
	var yamlData []byte
	if err == nil {
		yamlData, err = yaml.JSONToYAML(jsonBytes)
	}
	if err != nil {
		yamlData = []byte(fmt.Sprintf("error marshaling YAML: %v", err))
	}

	labels := make(map[string]string)
	for k, v := range pod.Labels {
		labels[k] = v
	}
	annotations := make(map[string]string)
	for k, v := range pod.Annotations {
		annotations[k] = v
	}

	return &PodDetail{
		Name:         pod.Name,
		Namespace:    pod.Namespace,
		Status:       status,
		NodeName:     pod.Spec.NodeName,
		PodIP:        pod.Status.PodIP,
		HostIP:       pod.Status.HostIP,
		CreationTime: pod.CreationTimestamp.Format("2006-01-02 15:04:05"),
		Age:          fmtDuration(now.Sub(pod.CreationTimestamp.Time)),
		Ready:        fmt.Sprintf("%d/%d", readyContainers, totalContainers),
		RestartCount: restarts,
		QosClass:     string(pod.Status.QOSClass),
		Containers:   containers,
		Conditions:   conditions,
		Events:       events,
		Labels:       labels,
		Annotations:  annotations,
		YAML:         string(yamlData),
	}, nil
}

func getPodEvents(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) ([]EventDetail, error) {
	eventsList, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + podName,
	})
	if err != nil {
		return nil, err
	}

	result := make([]EventDetail, 0, len(eventsList.Items))
	for _, e := range eventsList.Items {
		result = append(result, EventDetail{
			Type:      e.Type,
			Reason:    e.Reason,
			Message:   e.Message,
			FirstTime: e.FirstTimestamp.Format("2006-01-02 15:04:05"),
			LastTime:  e.LastTimestamp.Format("2006-01-02 15:04:05"),
			Count:     e.Count,
		})
	}
	if result == nil {
		result = []EventDetail{}
	}
	return result, nil
}

func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh%dm", h, m)
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, h)
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
