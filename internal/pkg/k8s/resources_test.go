package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPIServer 是一个只认精确路径的假 API Server，记录每个请求供断言请求形态。
type fakeAPIServer struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	reqs   []*http.Request
}

func newFakeAPIServer(t *testing.T) *fakeAPIServer {
	f := &fakeAPIServer{t: t, routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Clone(context.Background()))
		h, ok := f.routes[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			writeStatus(w, http.StatusNotFound, "NotFound", "the server could not find the requested resource")
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPIServer) handle(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = h
}

func (f *fakeAPIServer) json(path string, body string) {
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func (f *fakeAPIServer) requests(path string) []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*http.Request
	for _, r := range f.reqs {
		if r.URL.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeAPIServer) conn() *Conn { return testConn(f.t, kubeconfigFor(f.srv.URL)) }

// testConn 构造连接，测试结束时关闭。
func testConn(t *testing.T, kubeconfig string, opts ...ClientOption) *Conn {
	t.Helper()
	c, err := NewConn(kubeconfig, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func kubeconfigFor(server string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user: {}
`, server)
}

func writeStatus(w http.ResponseWriter, code int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"kind": "Status", "apiVersion": "v1", "status": "Failure",
		"reason": reason, "message": message, "code": code,
	})
}

func TestGetClusterVersionReadsOnlyVersion(t *testing.T) {
	f := newFakeAPIServer(t)
	f.json("/version", `{"gitVersion":"v1.31.2+k3s1","platform":"linux/amd64"}`)

	got, err := GetClusterVersion(context.Background(), f.conn())
	require.NoError(t, err)
	assert.Equal(t, &ClusterVersion{Version: "v1.31.2+k3s1", Platform: "linux/amd64"}, got)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		assert.Equal(t, "/version", r.URL.Path, "version read must not depend on any other endpoint")
	}
}

func serveDiscovery(f *fakeAPIServer) {
	f.json("/api", `{"kind":"APIVersions","versions":["v1"]}`)
	f.json("/apis", `{"kind":"APIGroupList","apiVersion":"v1","groups":[
	 {"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}},
	 {"name":"autoscaling","versions":[{"groupVersion":"autoscaling/v2","version":"v2"},{"groupVersion":"autoscaling/v1","version":"v1"}],"preferredVersion":{"groupVersion":"autoscaling/v2","version":"v2"}},
	 {"name":"coordination.k8s.io","versions":[{"groupVersion":"coordination.k8s.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"coordination.k8s.io/v1","version":"v1"}},
	 {"name":"stable.example.com","versions":[{"groupVersion":"stable.example.com/v1","version":"v1"}],"preferredVersion":{"groupVersion":"stable.example.com/v1","version":"v1"}},
	 {"name":"metrics.k8s.io","versions":[{"groupVersion":"metrics.k8s.io/v1beta1","version":"v1beta1"}],"preferredVersion":{"groupVersion":"metrics.k8s.io/v1beta1","version":"v1beta1"}}
	]}`)
	listVerbs := `["create","delete","get","list","patch","update","watch"]`
	f.json("/api/v1", `{"kind":"APIResourceList","groupVersion":"v1","resources":[
	 {"name":"pods","namespaced":true,"kind":"Pod","verbs":`+listVerbs+`},
	 {"name":"pods/log","namespaced":true,"kind":"Pod","verbs":["get"]},
	 {"name":"nodes","namespaced":false,"kind":"Node","verbs":`+listVerbs+`},
	 {"name":"bindings","namespaced":true,"kind":"Binding","verbs":["create"]},
	 {"name":"events","namespaced":true,"kind":"Event","verbs":`+listVerbs+`},
	 {"name":"endpoints","namespaced":true,"kind":"Endpoints","verbs":`+listVerbs+`}
	]}`)
	f.json("/apis/apps/v1", `{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[
	 {"name":"deployments","namespaced":true,"kind":"Deployment","verbs":`+listVerbs+`},
	 {"name":"deployments/scale","namespaced":true,"kind":"Scale","verbs":["get","patch","update"]}
	]}`)
	f.json("/apis/autoscaling/v2", `{"kind":"APIResourceList","groupVersion":"autoscaling/v2","resources":[
	 {"name":"horizontalpodautoscalers","namespaced":true,"kind":"HorizontalPodAutoscaler","verbs":`+listVerbs+`}
	]}`)
	f.json("/apis/autoscaling/v1", `{"kind":"APIResourceList","groupVersion":"autoscaling/v1","resources":[
	 {"name":"horizontalpodautoscalers","namespaced":true,"kind":"HorizontalPodAutoscaler","verbs":`+listVerbs+`}
	]}`)
	f.json("/apis/coordination.k8s.io/v1", `{"kind":"APIResourceList","groupVersion":"coordination.k8s.io/v1","resources":[
	 {"name":"leases","namespaced":true,"kind":"Lease","verbs":`+listVerbs+`}
	]}`)
	f.json("/apis/stable.example.com/v1", `{"kind":"APIResourceList","groupVersion":"stable.example.com/v1","resources":[
	 {"name":"crontabs","namespaced":true,"kind":"CronTab","verbs":`+listVerbs+`}
	]}`)
	f.handle("/apis/metrics.k8s.io/v1beta1", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusServiceUnavailable, "ServiceUnavailable", "metrics-server is down")
	})
}

func TestGetResourceTypesGroupsIntoCategoriesAndKeepsRestOnGroupFailure(t *testing.T) {
	f := newFakeAPIServer(t)
	serveDiscovery(f)

	got, err := GetResourceTypes(context.Background(), f.conn())
	require.NoError(t, err, "a failing API group must not fail the whole discovery")

	type key struct{ group, resource string }
	byKey := map[key]ResourceType{}
	for _, rt := range got.Types {
		byKey[key{rt.Group, rt.Resource}] = rt
	}

	assert.Equal(t, ResourceType{
		Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true,
		Verbs: []string{"create", "delete", "get", "list", "patch", "update", "watch"}, Category: CategoryWorkloads,
	}, byKey[key{"", "pods"}])
	assert.Equal(t, CategoryCluster, byKey[key{"", "nodes"}].Category)
	assert.False(t, byKey[key{"", "nodes"}].Namespaced)
	assert.Equal(t, CategoryCluster, byKey[key{"", "events"}].Category)
	assert.Equal(t, CategoryWorkloads, byKey[key{"apps", "deployments"}].Category)
	hpa := byKey[key{"autoscaling", "horizontalpodautoscalers"}]
	assert.Equal(t, CategoryConfig, hpa.Category)
	assert.Equal(t, "v2", hpa.Version, "the group's preferred version is used")
	// 规格未列出的内置类型与 CRD 都归到自定义资源，按 API 组区分。
	assert.Equal(t, CategoryCustom, byKey[key{"", "endpoints"}].Category)
	assert.Equal(t, CategoryCustom, byKey[key{"coordination.k8s.io", "leases"}].Category)
	assert.Equal(t, CategoryCustom, byKey[key{"stable.example.com", "crontabs"}].Category)

	assert.NotContains(t, byKey, key{"", "pods/log"}, "subresources are not types")
	assert.NotContains(t, byKey, key{"apps", "deployments/scale"}, "subresources are not types")
	assert.NotContains(t, byKey, key{"", "bindings"}, "types without the list verb are not shown")
	assert.Len(t, got.Types, 8, "each group/resource appears once")

	require.Len(t, got.FailedGroups, 1)
	assert.Equal(t, "metrics.k8s.io/v1beta1", got.FailedGroups[0].GroupVersion)
	assert.Contains(t, got.FailedGroups[0].Error, "metrics-server is down")

	// 类别按规格顺序排列：集群 → 工作负载 → … → 自定义。
	order := map[string]int{}
	for i, c := range []string{CategoryCluster, CategoryWorkloads, CategoryNetwork, CategoryConfig, CategoryStorage, CategoryRBAC, CategoryCustom} {
		order[c] = i
	}
	for i := 1; i < len(got.Types); i++ {
		assert.LessOrEqual(t, order[got.Types[i-1].Category], order[got.Types[i].Category], "types sorted by category")
	}
}

const podTableColumns = `[
 {"name":"Name","type":"string","format":"name","description":"","priority":0},
 {"name":"Ready","type":"string","format":"","description":"","priority":0},
 {"name":"Status","type":"string","format":"","description":"","priority":0},
 {"name":"Restarts","type":"string","format":"","description":"","priority":0},
 {"name":"Age","type":"string","format":"","description":"","priority":0},
 {"name":"IP","type":"string","format":"","description":"","priority":1},
 {"name":"Node","type":"string","format":"","description":"","priority":1}
]`

func podRow(name, ns, uid string, includeObject bool) string {
	meta := fmt.Sprintf(`{"name":%q,"namespace":%q,"uid":%q,"creationTimestamp":"2026-11-01T08:00:00Z","labels":{"app":"api"},
	 "ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"api-7d9","uid":"rs1","controller":true}],
	 "managedFields":[{"manager":"kubectl","operation":"Apply"}]}`, name, ns, uid)
	object := fmt.Sprintf(`{"kind":"PartialObjectMetadata","apiVersion":"meta.k8s.io/v1","metadata":%s}`, meta)
	if includeObject {
		object = fmt.Sprintf(`{"kind":"Pod","apiVersion":"v1","metadata":%s,"spec":{"nodeName":"n1"},"status":{"phase":"Running"}}`, meta)
	}
	return fmt.Sprintf(`{"cells":[%q,"1/1","Running",0,"15d","10.0.0.1","n1"],"object":%s}`, name, object)
}

func tableBody(continueToken string, rows ...string) string {
	return fmt.Sprintf(`{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"continue":%q},"columnDefinitions":%s,"rows":[%s]}`,
		continueToken, podTableColumns, strings.Join(rows, ","))
}

func TestListResourcesUsesServerSideTableAndMergesPages(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/api/v1/namespaces/default/pods"
	f.handle(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") == "" {
			_, _ = w.Write([]byte(tableBody("tok1", podRow("api-1", "default", "u1", false))))
			return
		}
		_, _ = w.Write([]byte(tableBody("", podRow("api-2", "default", "u2", false))))
	})

	got, err := ListResources(context.Background(), f.conn(), ListQuery{
		Version: "v1", Resource: "pods", Namespace: "default",
		LabelSelector: "app=api,tier!=db", FieldSelector: "status.phase=Running",
	})
	require.NoError(t, err)

	reqs := f.requests(path)
	require.Len(t, reqs, 2, "continue token must be followed")
	for i, r := range reqs {
		assert.Contains(t, r.Header.Get("Accept"), "application/json;as=Table;g=meta.k8s.io;v=v1")
		q := r.URL.Query()
		assert.Equal(t, "app=api,tier!=db", q.Get("labelSelector"))
		assert.Equal(t, "status.phase=Running", q.Get("fieldSelector"))
		assert.Equal(t, "500", q.Get("limit"))
		assert.Equal(t, "Metadata", q.Get("includeObject"))
		if i == 1 {
			assert.Equal(t, "tok1", q.Get("continue"))
		}
	}

	assert.Equal(t, []TableColumn{
		{Name: "Name", Type: "string", Format: "name"},
		{Name: "Ready", Type: "string"},
		{Name: "Status", Type: "string"},
		{Name: "Restarts", Type: "string"},
		{Name: "Age", Type: "string"},
	}, got.Columns, "only priority-0 columns, no namespace column inside one namespace")
	require.Len(t, got.Rows, 2)
	assert.Equal(t, ResourceRow{
		Name: "api-1", Namespace: "default", UID: "u1", CreationTimestamp: "2026-11-01T08:00:00Z",
		Labels:          map[string]string{"app": "api"},
		OwnerReferences: []OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-7d9", Controller: true}},
		Cells:           []any{"api-1", "1/1", "Running", float64(0), "15d"},
	}, got.Rows[0])
	assert.Equal(t, "api-2", got.Rows[1].Name)
}

func TestListResourcesAllNamespacesAddsNamespaceColumn(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/pods", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tableBody("", podRow("api-1", "prod", "u1", false), podRow("web-1", "dev", "u2", false))))
	})

	got, err := ListResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "pods"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(got.Columns), 2)
	assert.Equal(t, "Name", got.Columns[0].Name)
	assert.Equal(t, TableColumn{Name: "Namespace", Type: "string"}, got.Columns[1])
	assert.Equal(t, []any{"api-1", "prod", "1/1", "Running", float64(0), "15d"}, got.Rows[0].Cells)
	assert.Equal(t, "dev", got.Rows[1].Cells[1])
}

func TestListResourcesClusterScopedHasNoNamespaceColumn(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/nodes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tableBody("", podRow("n1", "", "u1", false))))
	})

	got, err := ListResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "nodes"})
	require.NoError(t, err)
	for _, c := range got.Columns {
		assert.NotEqual(t, "Namespace", c.Name)
	}
}

func TestListResourcesIncludeObjectStripsManagedFields(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/apis/stable.example.com/v1/namespaces/ns1/crontabs"
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tableBody("", podRow("c1", "ns1", "u1", true))))
	})

	got, err := ListResources(context.Background(), f.conn(), ListQuery{
		Group: "stable.example.com", Version: "v1", Resource: "crontabs", Namespace: "ns1", IncludeObject: true,
	})
	require.NoError(t, err)
	reqs := f.requests(path)
	require.Len(t, reqs, 1)
	assert.Equal(t, "Object", reqs[0].URL.Query().Get("includeObject"))
	require.Len(t, got.Rows, 1)
	obj := got.Rows[0].Object
	require.NotNil(t, obj)
	assert.Equal(t, map[string]any{"nodeName": "n1"}, obj["spec"])
	meta := obj["metadata"].(map[string]any)
	assert.Equal(t, "c1", meta["name"])
	assert.NotContains(t, meta, "managedFields")
}

// 列表行的状态判断以 Pod 的 status.phase 为准（Status 列在 CrashLoopBackOff 时是容器原因、不是 phase）：
// 读取时向服务端要对象，只把 phase 带回，不把整个对象发给前端。
func TestListResourcesWithPhaseCarriesStatusPhaseWithoutTheObject(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/api/v1/namespaces/default/pods"
	f.handle(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		withObject := r.URL.Query().Get("includeObject") == "Object"
		_, _ = w.Write([]byte(tableBody("", podRow("api-1", "default", "u1", withObject))))
	})

	got, err := ListResources(context.Background(), f.conn(), ListQuery{
		Version: "v1", Resource: "pods", Namespace: "default", WithPhase: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "Object", f.requests(path)[0].URL.Query().Get("includeObject"))
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "Running", got.Rows[0].Phase)
	assert.Nil(t, got.Rows[0].Object, "the object stays on the server side of the IPC")

	plain, err := ListResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "pods", Namespace: "default"})
	require.NoError(t, err)
	assert.Empty(t, plain.Rows[0].Phase)
	assert.Equal(t, "Metadata", f.requests(path)[1].URL.Query().Get("includeObject"), "without WithPhase the cheap metadata table is used")
}

func TestListResourcesReturnsClusterErrorVerbatim(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/default/pods", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusBadRequest, "BadRequest", "unable to parse requirement: found '=', expected: identifier")
	})

	_, err := ListResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "pods", Namespace: "default", LabelSelector: "=x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse requirement: found '=', expected: identifier")
}

func TestListResourcesGoesThroughCustomDial(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/pods", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tableBody("")))
	})
	target, err := url.Parse(f.srv.URL)
	require.NoError(t, err)

	var dialed []string
	var mu sync.Mutex
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, network, target.Host)
	}
	// kubeconfig 指向一个不可解析的主机，只有经自定义拨号才能连上。
	_, err = ListResources(context.Background(), testConn(t, kubeconfigFor("http://k8s.invalid:6443"), WithDial(dial)), ListQuery{Version: "v1", Resource: "pods"})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, dialed, "k8s.invalid:6443")
}

func metaListBody(continueToken string, remaining *int64, names ...string) string {
	items := make([]string, 0, len(names))
	for _, n := range names {
		items = append(items, fmt.Sprintf(`{"metadata":{"name":%q}}`, n))
	}
	rem := ""
	if remaining != nil {
		rem = fmt.Sprintf(`,"remainingItemCount":%d`, *remaining)
	}
	return fmt.Sprintf(`{"kind":"PartialObjectMetadataList","apiVersion":"meta.k8s.io/v1","metadata":{"continue":%q%s},"items":[%s]}`,
		continueToken, rem, strings.Join(items, ","))
}

func TestCountResourcesUsesRemainingItemCount(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/apis/apps/v1/namespaces/default/deployments"
	remaining := int64(41)
	f.json(path, metaListBody("tok", &remaining, "d1"))

	got, err := CountResources(context.Background(), f.conn(), ListQuery{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "default"})
	require.NoError(t, err)
	assert.Equal(t, &ResourceCount{Count: 42}, got)
	reqs := f.requests(path)
	require.Len(t, reqs, 1, "remainingItemCount answers the count in one cheap call")
	assert.Equal(t, "1", reqs[0].URL.Query().Get("limit"))
	assert.Contains(t, reqs[0].Header.Get("Accept"), "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1")
}

func TestCountResourcesPagesWhenRemainingItemCountMissing(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/api/v1/pods"
	f.handle(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("continue") {
		case "":
			_, _ = w.Write([]byte(metaListBody("t1", nil, "p1")))
		case "t1":
			_, _ = w.Write([]byte(metaListBody("t2", nil, "p2", "p3", "p4")))
		default:
			_, _ = w.Write([]byte(metaListBody("", nil, "p5")))
		}
	})

	got, err := CountResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "pods", LabelSelector: "app=api"})
	require.NoError(t, err)
	assert.Equal(t, int64(5), got.Count)
	reqs := f.requests(path)
	require.Len(t, reqs, 3)
	for _, r := range reqs {
		assert.Equal(t, "app=api", r.URL.Query().Get("labelSelector"))
	}
	assert.Equal(t, "500", reqs[1].URL.Query().Get("limit"))
}

func TestCountResourcesReturnsPermissionError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/secrets", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `secrets is forbidden: User "viewer" cannot list resource "secrets"`)
	})

	_, err := CountResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "secrets"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `User "viewer" cannot list resource "secrets"`)
}

func TestGetResourceStripsManagedFields(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/apis/apps/v1/namespaces/default/deployments/web"
	f.json(path, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"default",
	 "managedFields":[{"manager":"kubectl","operation":"Apply"}]},"spec":{"replicas":3}}`)

	got, err := GetResource(context.Background(), f.conn(), ResourceRef{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "default", Name: "web"})
	require.NoError(t, err)
	assert.False(t, got.NotFound)
	assert.Equal(t, map[string]any{"replicas": json.Number("3")}, got.Object["spec"], "numbers keep their exact value")
	assert.NotContains(t, got.Object["metadata"], "managedFields")
	assert.Contains(t, got.YAML, "name: web")
	assert.Contains(t, got.YAML, "replicas: 3")
	assert.NotContains(t, got.YAML, "managedFields")
	reqs := f.requests(path)
	require.Len(t, reqs, 1)
	assert.Contains(t, reqs[0].Header.Get("Accept"), "application/json")
}

func TestGetResourceReportsNotFound(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/default/pods/gone", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusNotFound, "NotFound", `pods "gone" not found`)
	})

	got, err := GetResource(context.Background(), f.conn(), ResourceRef{Version: "v1", Resource: "pods", Namespace: "default", Name: "gone"})
	require.NoError(t, err, "a deleted resource is a state, not an error")
	assert.Equal(t, &ResourceObject{NotFound: true}, got)
}

func TestGetResourceReturnsPermissionError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/default/secrets/s1", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `secrets "s1" is forbidden`)
	})

	_, err := GetResource(context.Background(), f.conn(), ResourceRef{Version: "v1", Resource: "secrets", Namespace: "default", Name: "s1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `secrets "s1" is forbidden`)
}

func TestListEventsFiltersByObjectAndSortsNewestFirst(t *testing.T) {
	f := newFakeAPIServer(t)
	path := "/api/v1/namespaces/default/events"
	f.handle(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		involved := `"involvedObject":{"apiVersion":"v1","kind":"Pod","namespace":"default","name":"api-1","uid":"u1"}`
		if r.URL.Query().Get("continue") == "" {
			_, _ = w.Write([]byte(`{"kind":"EventList","apiVersion":"v1","metadata":{"continue":"e1"},"items":[
			 {"metadata":{"name":"a"},"type":"Warning","reason":"BackOff","message":"old","count":3,"lastTimestamp":"2026-11-10T08:00:00Z",` + involved + `}
			]}`))
			return
		}
		_, _ = w.Write([]byte(`{"kind":"EventList","apiVersion":"v1","metadata":{},"items":[
		 {"metadata":{"name":"b"},"type":"Warning","reason":"Failed","message":"newest","count":1,"lastTimestamp":"2026-11-12T08:00:00Z",` + involved + `},
		 {"metadata":{"name":"c"},"type":"Warning","reason":"Unhealthy","message":"middle","eventTime":"2026-11-11T08:00:00.000000Z","series":{"count":7,"lastObservedTime":"2026-11-11T09:00:00.000000Z"},` + involved + `}
		]}`))
	})

	got, err := ListEvents(context.Background(), f.conn(), EventQuery{Namespace: "default", InvolvedKind: "Pod", InvolvedName: "api-1", WarningOnly: true})
	require.NoError(t, err)

	reqs := f.requests(path)
	require.Len(t, reqs, 2, "event pages are merged")
	assert.Equal(t, "involvedObject.kind=Pod,involvedObject.name=api-1,type=Warning", reqs[0].URL.Query().Get("fieldSelector"))
	assert.Equal(t, "e1", reqs[1].URL.Query().Get("continue"))

	require.Len(t, got.Events, 3)
	assert.Equal(t, []string{"newest", "middle", "old"}, []string{got.Events[0].Message, got.Events[1].Message, got.Events[2].Message})
	assert.Equal(t, Event{
		Type: "Warning", Reason: "Failed", Message: "newest", Count: 1, LastTimestamp: "2026-11-12T08:00:00Z",
		InvolvedObject: EventObject{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "api-1", UID: "u1"},
	}, got.Events[0])
	assert.Equal(t, int32(7), got.Events[1].Count, "series count for new-style events")
	assert.Equal(t, "2026-11-11T09:00:00Z", got.Events[1].LastTimestamp, "series last observed time for new-style events")
}

func TestListEventsAllNamespacesWithoutFilters(t *testing.T) {
	f := newFakeAPIServer(t)
	f.json("/api/v1/events", `{"kind":"EventList","apiVersion":"v1","metadata":{},"items":[]}`)

	got, err := ListEvents(context.Background(), f.conn(), EventQuery{})
	require.NoError(t, err)
	assert.NotNil(t, got.Events)
	assert.Empty(t, got.Events)
	reqs := f.requests("/api/v1/events")
	require.Len(t, reqs, 1)
	assert.Equal(t, "", reqs[0].URL.Query().Get("fieldSelector"))
}

func TestListResourcesRejectsRowWithMissingCells(t *testing.T) {
	f := newFakeAPIServer(t)
	f.json("/api/v1/pods", fmt.Sprintf(`{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{},"columnDefinitions":%s,
	 "rows":[{"cells":["api-1"],"object":{"metadata":{"name":"api-1","namespace":"default"}}}]}`, podTableColumns))

	_, err := ListResources(context.Background(), f.conn(), ListQuery{Version: "v1", Resource: "pods"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cells")
}
