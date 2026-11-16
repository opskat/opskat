package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 集群健康区块名，同时是 ClusterHealth.Errors 的键。
const (
	sectionNodes        = "nodes"
	sectionPods         = "pods"
	sectionDeployments  = "deployments"
	sectionStatefulSets = "statefulsets"
	sectionDaemonSets   = "daemonsets"
	sectionJobs         = "jobs"
	sectionEvents       = "events"
)

type NodeHealth struct {
	Total         int    `json:"total"`
	Schedulable   int    `json:"schedulable"`
	Unschedulable int    `json:"unschedulable"`
	NotReady      int    `json:"notReady"`
	Error         string `json:"error,omitempty"`
}

type PodHealth struct {
	Total    int    `json:"total"`
	Running  int    `json:"running"`
	Pending  int    `json:"pending"`
	Abnormal int    `json:"abnormal"`
	Error    string `json:"error,omitempty"`
}

// AttentionItem 是「需要关注」里的一行：Status 是一眼可读的状态（"1/3"、"Failed"、"CrashLoopBackOff"），Reason 是原因。
type AttentionItem struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
}

// ClusterHealth 是集群概览与侧栏异常数的唯一来源，「异常」的判定都在这里。
// 各区块独立读取：某区块失败只记入 Errors（节点 / Pod 另在各自结构里带 Error），不影响其它区块；
// 读取失败的类型不出现在 AbnormalCounts，而不是记为 0。
type ClusterHealth struct {
	Nodes          NodeHealth        `json:"nodes"`
	Pods           PodHealth         `json:"pods"`
	Attention      []AttentionItem   `json:"attention"`
	AbnormalCounts map[string]int    `json:"abnormalCounts"`
	WarningEvents  int               `json:"warningEvents"`
	Errors         map[string]string `json:"errors"`
}

// listItems 分批列出某类资源并按 T 解码。
func listItems[T any](ctx context.Context, c *apiClient, q ListQuery) ([]T, error) {
	var items []T
	continueToken := ""
	for {
		raw, err := c.listPage(ctx, q, "application/json", listChunkSize, continueToken, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Metadata metav1.ListMeta `json:"metadata"`
			Items    []T             `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decode %s list: %w", q.Resource, err)
		}
		items = append(items, page.Items...)
		if page.Metadata.Continue == "" {
			return items, nil
		}
		continueToken = page.Metadata.Continue
	}
}

// GetClusterHealth 读取节点 / Pod / 工作负载 / Job / 告警事件并判定异常；namespace 为空表示全部命名空间（节点始终是全集群）。
func GetClusterHealth(ctx context.Context, kubeconfig, namespace string, opts ...ClientOption) (*ClusterHealth, error) {
	c, err := newAPIClient(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	h := &ClusterHealth{Attention: []AttentionItem{}, AbnormalCounts: map[string]int{}, Errors: map[string]string{}}

	var mu sync.Mutex
	var wg sync.WaitGroup
	// section 并发读取一个区块；apply 在锁内把读到的结果并入 h，失败只记在这个区块名下。
	section := func(name string, read func() (apply func(), err error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			apply, err := read()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				h.Errors[name] = err.Error()
				return
			}
			apply()
		}()
	}

	section(sectionNodes, func() (func(), error) {
		nodes, err := listItems[corev1.Node](ctx, c, ListQuery{Version: "v1", Resource: "nodes"})
		if err != nil {
			return nil, err
		}
		return func() {
			h.Nodes, h.AbnormalCounts["/nodes"] = summarizeNodes(nodes)
		}, nil
	})
	section(sectionPods, func() (func(), error) {
		pods, err := listItems[corev1.Pod](ctx, c, ListQuery{Version: "v1", Resource: "pods", Namespace: namespace})
		if err != nil {
			return nil, err
		}
		return func() {
			var items []AttentionItem
			h.Pods, items = summarizePods(pods)
			h.Attention = append(h.Attention, items...)
			h.AbnormalCounts["/pods"] = len(items)
		}, nil
	})
	section(sectionDeployments, func() (func(), error) {
		list, err := listItems[appsv1.Deployment](ctx, c, ListQuery{Group: "apps", Version: "v1", Resource: "deployments", Namespace: namespace})
		if err != nil {
			return nil, err
		}
		return func() {
			var items []AttentionItem
			for _, d := range list {
				desired := int32(1)
				if d.Spec.Replicas != nil {
					desired = *d.Spec.Replicas
				}
				reason := ""
				for _, cond := range d.Status.Conditions {
					if cond.Status == corev1.ConditionFalse && cond.Type == appsv1.DeploymentAvailable {
						reason = cond.Reason
					}
					if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue {
						reason = cond.Reason
					}
				}
				items = appendWorkload(items, "Deployment", "deployments", d.ObjectMeta, desired, d.Status.ReadyReplicas, reason)
			}
			h.Attention = append(h.Attention, items...)
			h.AbnormalCounts["apps/deployments"] = len(items)
		}, nil
	})
	section(sectionStatefulSets, func() (func(), error) {
		list, err := listItems[appsv1.StatefulSet](ctx, c, ListQuery{Group: "apps", Version: "v1", Resource: "statefulsets", Namespace: namespace})
		if err != nil {
			return nil, err
		}
		return func() {
			var items []AttentionItem
			for _, s := range list {
				desired := int32(1)
				if s.Spec.Replicas != nil {
					desired = *s.Spec.Replicas
				}
				items = appendWorkload(items, "StatefulSet", "statefulsets", s.ObjectMeta, desired, s.Status.ReadyReplicas, "")
			}
			h.Attention = append(h.Attention, items...)
			h.AbnormalCounts["apps/statefulsets"] = len(items)
		}, nil
	})
	section(sectionDaemonSets, func() (func(), error) {
		list, err := listItems[appsv1.DaemonSet](ctx, c, ListQuery{Group: "apps", Version: "v1", Resource: "daemonsets", Namespace: namespace})
		if err != nil {
			return nil, err
		}
		return func() {
			var items []AttentionItem
			for _, d := range list {
				items = appendWorkload(items, "DaemonSet", "daemonsets", d.ObjectMeta, d.Status.DesiredNumberScheduled, d.Status.NumberReady, "")
			}
			h.Attention = append(h.Attention, items...)
			h.AbnormalCounts["apps/daemonsets"] = len(items)
		}, nil
	})
	section(sectionJobs, func() (func(), error) {
		list, err := listItems[batchv1.Job](ctx, c, ListQuery{Group: "batch", Version: "v1", Resource: "jobs", Namespace: namespace})
		if err != nil {
			return nil, err
		}
		return func() {
			var items []AttentionItem
			for _, j := range list {
				for _, cond := range j.Status.Conditions {
					if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
						items = append(items, AttentionItem{
							Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job",
							Namespace: j.Namespace, Name: j.Name, Status: "Failed", Reason: cond.Reason,
						})
						break
					}
				}
			}
			h.Attention = append(h.Attention, items...)
			h.AbnormalCounts["batch/jobs"] = len(items)
		}, nil
	})
	section(sectionEvents, func() (func(), error) {
		events, err := ListEvents(ctx, kubeconfig, EventQuery{Namespace: namespace, WarningOnly: true}, opts...)
		if err != nil {
			return nil, err
		}
		return func() { h.WarningEvents = len(events.Events) }, nil
	})
	wg.Wait()

	h.Nodes.Error = h.Errors[sectionNodes]
	h.Pods.Error = h.Errors[sectionPods]
	sortAttention(h.Attention)
	return h, nil
}

// appendWorkload 记录就绪副本少于期望（期望 > 0）的工作负载。
func appendWorkload(items []AttentionItem, kind, resource string, meta metav1.ObjectMeta, desired, ready int32, reason string) []AttentionItem {
	if desired <= 0 || ready >= desired {
		return items
	}
	if reason == "" {
		reason = "ReplicasUnavailable"
	}
	return append(items, AttentionItem{
		Group: "apps", Version: "v1", Resource: resource, Kind: kind,
		Namespace: meta.Namespace, Name: meta.Name, Status: fmt.Sprintf("%d/%d", ready, desired), Reason: reason,
	})
}

// summarizeNodes 返回节点汇总与异常数（未就绪或停止调度）。
func summarizeNodes(nodes []corev1.Node) (NodeHealth, int) {
	n := NodeHealth{Total: len(nodes)}
	abnormal := 0
	for _, node := range nodes {
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				ready = cond.Status == corev1.ConditionTrue
			}
		}
		if node.Spec.Unschedulable {
			n.Unschedulable++
		} else {
			n.Schedulable++
		}
		if !ready {
			n.NotReady++
		}
		if !ready || node.Spec.Unschedulable {
			abnormal++
		}
	}
	return n, abnormal
}

func summarizePods(pods []corev1.Pod) (PodHealth, []AttentionItem) {
	p := PodHealth{Total: len(pods)}
	var items []AttentionItem
	for _, pod := range pods {
		switch pod.Status.Phase {
		case corev1.PodRunning:
			p.Running++
		case corev1.PodPending:
			p.Pending++
		}
		if status, reason, bad := podProblem(pod); bad {
			items = append(items, AttentionItem{
				Version: "v1", Resource: "pods", Kind: "Pod",
				Namespace: pod.Namespace, Name: pod.Name, Status: status, Reason: reason,
			})
		}
	}
	p.Abnormal = len(items)
	return p, items
}

// podProblem 判定 Pod 是否异常：容器处于等待且原因不是 ContainerCreating / PodInitializing，
// 或上次以非 0 退出且仍未就绪（仍在重启）；否则看阶段是否为 Failed / Unknown / Pending。
func podProblem(pod corev1.Pod) (status, reason string, abnormal bool) {
	statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
			detail := w.Reason
			if t := cs.LastTerminationState.Terminated; t != nil && t.ExitCode != 0 {
				detail = fmt.Sprintf("%s (exit %d, restarts %d)", w.Reason, t.ExitCode, cs.RestartCount)
			}
			return w.Reason, cs.Name + ": " + detail, true
		}
	}
	for _, cs := range statuses {
		if t := cs.LastTerminationState.Terminated; t != nil && t.ExitCode != 0 && !cs.Ready {
			return "Restarting", fmt.Sprintf("%s: exit %d, restarts %d", cs.Name, t.ExitCode, cs.RestartCount), true
		}
	}
	switch pod.Status.Phase {
	case corev1.PodFailed, corev1.PodUnknown, corev1.PodPending:
		reason = pod.Status.Reason
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && reason == "" {
				reason = cond.Reason
			}
		}
		return string(pod.Status.Phase), reason, true
	}
	return "", "", false
}

// sortAttention 让清单稳定：按命名空间、类型、名称排序（各区块并发读取，到达顺序不定）。
func sortAttention(items []AttentionItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
}
