package k8s

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func itemsList(kind string, items ...string) string {
	body := ""
	for i, it := range items {
		if i > 0 {
			body += ","
		}
		body += it
	}
	return fmt.Sprintf(`{"kind":%q,"apiVersion":"v1","metadata":{},"items":[%s]}`, kind, body)
}

func podJSON(name, phase, statuses string) string {
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":"default"},"status":{"phase":%q,"containerStatuses":[%s]}}`, name, phase, statuses)
}

func nodeJSON(name string, ready string, unschedulable bool) string {
	return fmt.Sprintf(`{"metadata":{"name":%q},"spec":{"unschedulable":%t},"status":{"conditions":[{"type":"Ready","status":%q}]}}`, name, unschedulable, ready)
}

// serveHealthy 注册各区块的空列表，单个用例再覆盖要测的那一块。
func serveHealthy(f *fakeAPIServer) {
	f.json("/api/v1/nodes", itemsList("NodeList"))
	f.json("/api/v1/pods", itemsList("PodList"))
	f.json("/apis/apps/v1/deployments", itemsList("DeploymentList"))
	f.json("/apis/apps/v1/statefulsets", itemsList("StatefulSetList"))
	f.json("/apis/apps/v1/daemonsets", itemsList("DaemonSetList"))
	f.json("/apis/batch/v1/jobs", itemsList("JobList"))
	f.json("/api/v1/events", itemsList("EventList"))
}

func TestGetClusterHealthClassifiesPods(t *testing.T) {
	f := newFakeAPIServer(t)
	serveHealthy(f)
	f.json("/api/v1/pods", itemsList("PodList",
		podJSON("ok", "Running", `{"name":"c","ready":true,"restartCount":0,"state":{"running":{}}}`),
		podJSON("done", "Succeeded", ``),
		podJSON("failed", "Failed", ``),
		podJSON("unknown", "Unknown", ``),
		podJSON("pending", "Pending", ``),
		podJSON("creating", "Running", `{"name":"c","ready":false,"state":{"waiting":{"reason":"ContainerCreating"}}}`),
		podJSON("initing", "Running", `{"name":"c","ready":false,"state":{"waiting":{"reason":"PodInitializing"}}}`),
		podJSON("backoff", "Running", `{"name":"api","ready":false,"restartCount":14,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"exitCode":1}}}`),
		podJSON("imagepull", "Running", `{"name":"c","ready":false,"state":{"waiting":{"reason":"ErrImagePull"}}}`),
		podJSON("restarting", "Running", `{"name":"c","ready":false,"restartCount":2,"state":{"running":{}},"lastState":{"terminated":{"exitCode":137}}}`),
		podJSON("recovered", "Running", `{"name":"c","ready":true,"restartCount":2,"state":{"running":{}},"lastState":{"terminated":{"exitCode":137}}}`),
		podJSON("cleanexit", "Running", `{"name":"c","ready":false,"restartCount":1,"state":{"running":{}},"lastState":{"terminated":{"exitCode":0}}}`),
	))

	got, err := GetClusterHealth(context.Background(), f.kubeconfig(), "")
	require.NoError(t, err)

	assert.Equal(t, 12, got.Pods.Total)
	assert.Equal(t, 8, got.Pods.Running)
	assert.Equal(t, 1, got.Pods.Pending)
	names := make([]string, 0, len(got.Attention))
	for _, a := range got.Attention {
		names = append(names, a.Name)
		assert.Equal(t, "Pod", a.Kind)
		assert.Equal(t, "pods", a.Resource)
		assert.Equal(t, "v1", a.Version)
		assert.Equal(t, "default", a.Namespace)
	}
	assert.ElementsMatch(t, []string{"failed", "unknown", "pending", "backoff", "imagepull", "restarting"}, names)
	assert.Equal(t, 6, got.Pods.Abnormal)
	assert.Equal(t, 6, got.AbnormalCounts["/pods"])
	for _, a := range got.Attention {
		if a.Name == "backoff" {
			assert.Equal(t, "CrashLoopBackOff", a.Status)
			assert.Contains(t, a.Reason, "api")
		}
	}
}

func TestGetClusterHealthNodes(t *testing.T) {
	f := newFakeAPIServer(t)
	serveHealthy(f)
	f.json("/api/v1/nodes", itemsList("NodeList",
		nodeJSON("a", "True", false),
		nodeJSON("b", "True", true),
		nodeJSON("c", "False", false),
		nodeJSON("d", "Unknown", true),
	))

	got, err := GetClusterHealth(context.Background(), f.kubeconfig(), "")
	require.NoError(t, err)
	assert.Equal(t, 4, got.Nodes.Total)
	assert.Equal(t, 2, got.Nodes.Schedulable)
	assert.Equal(t, 2, got.Nodes.Unschedulable)
	assert.Equal(t, 2, got.Nodes.NotReady)
	assert.Equal(t, 3, got.AbnormalCounts["/nodes"], "NotReady or unschedulable")
	assert.Empty(t, got.Attention, "nodes are shown as cards, not in the attention list")
}

func TestGetClusterHealthWorkloadsAndJobs(t *testing.T) {
	f := newFakeAPIServer(t)
	serveHealthy(f)
	f.json("/apis/apps/v1/deployments", itemsList("DeploymentList",
		`{"metadata":{"name":"short","namespace":"prod"},"spec":{"replicas":3},"status":{"readyReplicas":1,"conditions":[{"type":"Available","status":"False","reason":"MinimumReplicasUnavailable"}]}}`,
		`{"metadata":{"name":"full","namespace":"prod"},"spec":{"replicas":2},"status":{"readyReplicas":2}}`,
		`{"metadata":{"name":"scaled0","namespace":"prod"},"spec":{"replicas":0},"status":{}}`,
		`{"metadata":{"name":"defaulted","namespace":"prod"},"spec":{},"status":{}}`,
	))
	f.json("/apis/apps/v1/statefulsets", itemsList("StatefulSetList",
		`{"metadata":{"name":"db","namespace":"prod"},"spec":{"replicas":2},"status":{"readyReplicas":0}}`,
	))
	f.json("/apis/apps/v1/daemonsets", itemsList("DaemonSetList",
		`{"metadata":{"name":"agent","namespace":"kube-system"},"status":{"desiredNumberScheduled":4,"numberReady":3}}`,
		`{"metadata":{"name":"idle","namespace":"kube-system"},"status":{"desiredNumberScheduled":0,"numberReady":0}}`,
	))
	f.json("/apis/batch/v1/jobs", itemsList("JobList",
		`{"metadata":{"name":"bad","namespace":"prod"},"status":{"conditions":[{"type":"Failed","status":"True","reason":"BackoffLimitExceeded"}]}}`,
		`{"metadata":{"name":"good","namespace":"prod"},"status":{"conditions":[{"type":"Complete","status":"True"}]}}`,
		`{"metadata":{"name":"retrying","namespace":"prod"},"status":{"failed":1,"active":1}}`,
	))

	got, err := GetClusterHealth(context.Background(), f.kubeconfig(), "")
	require.NoError(t, err)

	byName := map[string]AttentionItem{}
	for _, a := range got.Attention {
		byName[a.Name] = a
	}
	assert.Len(t, byName, 5)
	assert.Equal(t, AttentionItem{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "prod", Name: "short", Status: "1/3", Reason: "MinimumReplicasUnavailable"}, byName["short"])
	assert.Equal(t, "0/1", byName["defaulted"].Status, "spec.replicas defaults to 1")
	assert.Equal(t, "0/2", byName["db"].Status)
	assert.Equal(t, "StatefulSet", byName["db"].Kind)
	assert.Equal(t, "3/4", byName["agent"].Status)
	assert.Equal(t, "DaemonSet", byName["agent"].Kind)
	assert.Equal(t, AttentionItem{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "prod", Name: "bad", Status: "Failed", Reason: "BackoffLimitExceeded"}, byName["bad"])
	assert.NotContains(t, byName, "full")
	assert.NotContains(t, byName, "scaled0", "desired == 0 is not abnormal")
	assert.NotContains(t, byName, "idle")
	assert.NotContains(t, byName, "good")
	assert.NotContains(t, byName, "retrying", "only a Failed job is abnormal")

	assert.Equal(t, 2, got.AbnormalCounts["apps/deployments"])
	assert.Equal(t, 1, got.AbnormalCounts["apps/statefulsets"])
	assert.Equal(t, 1, got.AbnormalCounts["apps/daemonsets"])
	assert.Equal(t, 1, got.AbnormalCounts["batch/jobs"])
}

func TestGetClusterHealthScopesToNamespaceExceptNodes(t *testing.T) {
	f := newFakeAPIServer(t)
	f.json("/api/v1/nodes", itemsList("NodeList", nodeJSON("a", "True", false)))
	f.json("/api/v1/namespaces/prod/pods", itemsList("PodList", podJSON("p", "Failed", ``)))
	f.json("/apis/apps/v1/namespaces/prod/deployments", itemsList("DeploymentList"))
	f.json("/apis/apps/v1/namespaces/prod/statefulsets", itemsList("StatefulSetList"))
	f.json("/apis/apps/v1/namespaces/prod/daemonsets", itemsList("DaemonSetList"))
	f.json("/apis/batch/v1/namespaces/prod/jobs", itemsList("JobList"))
	f.json("/api/v1/namespaces/prod/events", itemsList("EventList",
		`{"metadata":{"name":"e1"},"type":"Warning","reason":"BackOff","lastTimestamp":"2026-11-10T08:00:00Z"}`,
		`{"metadata":{"name":"e2"},"type":"Warning","reason":"Failed","lastTimestamp":"2026-11-10T09:00:00Z"}`,
	))

	got, err := GetClusterHealth(context.Background(), f.kubeconfig(), "prod")
	require.NoError(t, err)
	assert.Equal(t, 1, got.Nodes.Total)
	assert.Equal(t, 1, got.Pods.Total)
	assert.Equal(t, 2, got.WarningEvents)
	assert.Empty(t, got.Errors)
	reqs := f.requests("/api/v1/namespaces/prod/events")
	require.Len(t, reqs, 1)
	assert.Equal(t, "type=Warning", reqs[0].URL.Query().Get("fieldSelector"))
}

func TestGetClusterHealthIsolatesForbiddenSections(t *testing.T) {
	f := newFakeAPIServer(t)
	serveHealthy(f)
	f.json("/api/v1/pods", itemsList("PodList", podJSON("p", "Failed", ``), podJSON("q", "Running", ``)))
	f.handle("/api/v1/nodes", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `nodes is forbidden: User "u" cannot list resource "nodes"`)
	})
	f.handle("/apis/batch/v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `jobs.batch is forbidden`)
	})

	got, err := GetClusterHealth(context.Background(), f.kubeconfig(), "")
	require.NoError(t, err, "a forbidden section must not fail the others")

	assert.Contains(t, got.Nodes.Error, "forbidden")
	assert.Equal(t, 0, got.Nodes.Total)
	assert.Empty(t, got.Pods.Error)
	assert.Equal(t, 2, got.Pods.Total)
	assert.Equal(t, 1, got.Pods.Abnormal)
	assert.Contains(t, got.Errors["nodes"], "forbidden")
	assert.Contains(t, got.Errors["jobs"], "forbidden")
	assert.NotContains(t, got.Errors, "pods")
	assert.NotContains(t, got.AbnormalCounts, "/nodes", "a count that could not be read is absent, not 0")
	assert.NotContains(t, got.AbnormalCounts, "batch/jobs")
	assert.Equal(t, 1, got.AbnormalCounts["/pods"])
	assert.Len(t, got.Attention, 1)
}
