package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

// 资源类型的侧栏类别，取值与前端约定一致。
const (
	CategoryCluster   = "cluster"
	CategoryWorkloads = "workloads"
	CategoryNetwork   = "network"
	CategoryConfig    = "config"
	CategoryStorage   = "storage"
	CategoryRBAC      = "rbac"
	CategoryCustom    = "custom"
)

type ClusterVersion struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

type ResourceType struct {
	Group      string   `json:"group"`
	Version    string   `json:"version"`
	Resource   string   `json:"resource"`
	Kind       string   `json:"kind"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs"`
	Category   string   `json:"category"`
}

type FailedGroup struct {
	GroupVersion string `json:"groupVersion"`
	Error        string `json:"error"`
}

type ResourceTypes struct {
	Types        []ResourceType `json:"types"`
	FailedGroups []FailedGroup  `json:"failedGroups"`
}

// ListQuery 描述一次列表/计数：Namespace 为空表示全部命名空间（集群级类型也传空）。
type ListQuery struct {
	Group         string
	Version       string
	Resource      string
	Namespace     string
	LabelSelector string
	FieldSelector string
	IncludeObject bool
	// WithPhase 让行带上对象的 status.phase（有该字段的类型，如 Pod）：向服务端要完整对象，但只回传 phase。
	WithPhase bool
}

type TableColumn struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Format string `json:"format"`
}

type OwnerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Controller bool   `json:"controller"`
}

type ResourceRow struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	UID               string            `json:"uid"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences"`
	Cells             []any             `json:"cells"`
	Phase             string            `json:"phase,omitempty"`
	Object            map[string]any    `json:"object,omitempty"`
}

type ResourceList struct {
	Columns []TableColumn `json:"columns"`
	Rows    []ResourceRow `json:"rows"`
}

type ResourceCount struct {
	Count int64 `json:"count"`
}

type ResourceRef struct {
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
}

type ResourceObject struct {
	NotFound bool           `json:"notFound"`
	Object   map[string]any `json:"object"`
	YAML     string         `json:"yaml"`
}

// EventQuery：Namespace 为空表示全部命名空间；InvolvedKind / InvolvedName 非空时只取该对象的事件。
// 按类型 + 名称而不是 UID 匹配（同 `kubectl events --for kind/name`）：kubelet 记录节点事件时 UID 填的是节点名。
type EventQuery struct {
	Namespace    string
	InvolvedKind string
	InvolvedName string
	WarningOnly  bool
}

type EventObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type Event struct {
	Type           string      `json:"type"`
	Reason         string      `json:"reason"`
	Message        string      `json:"message"`
	Count          int32       `json:"count"`
	LastTimestamp  string      `json:"lastTimestamp"`
	InvolvedObject EventObject `json:"involvedObject"`
}

type EventList struct {
	Events []Event `json:"events"`
}

// builtinCategories 只收录规格侧栏点名的内置类型，顺序即类别内的显示顺序。
// 其余可列出的类型（规格未点名的内置类型如 endpoints / leases，以及全部 CRD）一律归入 custom、
// 由前端按 API 组分组——对应规格「自定义资源：其余可列出的类型」，不在这里逐个挑拣内置组。
var builtinCategories = []struct{ group, resource, category string }{
	{"", "nodes", CategoryCluster},
	{"", "namespaces", CategoryCluster},
	{"", "events", CategoryCluster},
	{"", "pods", CategoryWorkloads},
	{"apps", "deployments", CategoryWorkloads},
	{"apps", "statefulsets", CategoryWorkloads},
	{"apps", "daemonsets", CategoryWorkloads},
	{"apps", "replicasets", CategoryWorkloads},
	{"batch", "jobs", CategoryWorkloads},
	{"batch", "cronjobs", CategoryWorkloads},
	{"", "services", CategoryNetwork},
	{"networking.k8s.io", "ingresses", CategoryNetwork},
	{"discovery.k8s.io", "endpointslices", CategoryNetwork},
	{"networking.k8s.io", "networkpolicies", CategoryNetwork},
	{"", "configmaps", CategoryConfig},
	{"", "secrets", CategoryConfig},
	{"autoscaling", "horizontalpodautoscalers", CategoryConfig},
	{"", "resourcequotas", CategoryConfig},
	{"", "limitranges", CategoryConfig},
	{"", "persistentvolumeclaims", CategoryStorage},
	{"", "persistentvolumes", CategoryStorage},
	{"storage.k8s.io", "storageclasses", CategoryStorage},
	{"", "serviceaccounts", CategoryRBAC},
	{"rbac.authorization.k8s.io", "roles", CategoryRBAC},
	{"rbac.authorization.k8s.io", "rolebindings", CategoryRBAC},
	{"rbac.authorization.k8s.io", "clusterroles", CategoryRBAC},
	{"rbac.authorization.k8s.io", "clusterrolebindings", CategoryRBAC},
}

var categoryOrder = []string{CategoryCluster, CategoryWorkloads, CategoryNetwork, CategoryConfig, CategoryStorage, CategoryRBAC, CategoryCustom}

const (
	// listChunkSize 是分批列出时每页的条数。
	listChunkSize = 500

	tableAccept        = "application/json;as=Table;g=meta.k8s.io;v=v1"
	metadataListAccept = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1,application/json"
)

// apiClient 是访问层的读客户端：discovery 与原始 REST 请求共用连接的 HTTP 客户端。
type apiClient struct {
	discovery *discovery.DiscoveryClient
	rest      rest.Interface
}

func newAPIClient(conn *Conn) (*apiClient, error) {
	disc, err := discovery.NewDiscoveryClientForConfigAndClient(conn.config, conn.client)
	if err != nil {
		return nil, fmt.Errorf("create k8s discovery client: %w", err)
	}
	return &apiClient{discovery: disc, rest: disc.RESTClient()}, nil
}

// body 取响应体；失败时经 Result.Error 解出集群返回的 Status，让无权限、选择器语法错误等原因原样上报
// （Result.Raw 只给出按状态码合成的笼统错误）。
func body(ctx context.Context, req *rest.Request) ([]byte, error) {
	result := req.Do(ctx)
	if err := result.Error(); err != nil {
		return nil, err
	}
	return result.Raw()
}

// get 构造对某类资源（namespace 为空即全部命名空间或集群级）的 GET 请求。
func (c *apiClient) get(group, version, resource, namespace string) *rest.Request {
	prefix := "/api/" + version
	if group != "" {
		prefix = "/apis/" + group + "/" + version
	}
	req := c.rest.Get().AbsPath(prefix).Resource(resource)
	if namespace != "" {
		req = req.Namespace(namespace)
	}
	return req
}

// listPage 发出一页列表请求，标签 / 字段选择器原样透传给集群。
func (c *apiClient) listPage(ctx context.Context, q ListQuery, accept string, limit int64, continueToken string, extra map[string]string) ([]byte, error) {
	req := c.get(q.Group, q.Version, q.Resource, q.Namespace).
		SetHeader("Accept", accept).
		Param("limit", strconv.FormatInt(limit, 10))
	if q.LabelSelector != "" {
		req = req.Param("labelSelector", q.LabelSelector)
	}
	if q.FieldSelector != "" {
		req = req.Param("fieldSelector", q.FieldSelector)
	}
	if continueToken != "" {
		req = req.Param("continue", continueToken)
	}
	for k, v := range extra {
		req = req.Param(k, v)
	}
	return body(ctx, req)
}

// GetClusterVersion 只读取 /version：页面打开唯一依赖的读取，不受其它接口失败影响。
func GetClusterVersion(ctx context.Context, conn *Conn) (*ClusterVersion, error) {
	c, err := newAPIClient(conn)
	if err != nil {
		return nil, err
	}
	info, err := c.discovery.ServerVersionWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get server version: %w", err)
	}
	return &ClusterVersion{Version: info.GitVersion, Platform: info.Platform}, nil
}

// GetResourceTypes 列出集群提供的可列出类型（每个组取首选版本），按类别归组排序；
// 某个 API 组发现失败只记入 FailedGroups，其余照常返回。
func GetResourceTypes(ctx context.Context, conn *Conn) (*ResourceTypes, error) {
	c, err := newAPIClient(conn)
	if err != nil {
		return nil, err
	}
	lists, err := c.discovery.ServerPreferredResourcesWithContext(ctx)
	result := &ResourceTypes{Types: []ResourceType{}, FailedGroups: []FailedGroup{}}
	if err != nil {
		var groupErr *discovery.ErrGroupDiscoveryFailed
		if !errors.As(err, &groupErr) {
			return nil, fmt.Errorf("discover API resources: %w", err)
		}
		for gv, gerr := range groupErr.Groups {
			result.FailedGroups = append(result.FailedGroups, FailedGroup{GroupVersion: gv.String(), Error: gerr.Error()})
		}
		sort.Slice(result.FailedGroups, func(i, j int) bool {
			return result.FailedGroups[i].GroupVersion < result.FailedGroups[j].GroupVersion
		})
	}

	builtinRank := make(map[schema.GroupResource]int, len(builtinCategories))
	builtinCategory := make(map[schema.GroupResource]string, len(builtinCategories))
	for i, b := range builtinCategories {
		gr := schema.GroupResource{Group: b.group, Resource: b.resource}
		builtinRank[gr] = i
		builtinCategory[gr] = b.category
	}

	// ServerPreferredResources 已剔除子资源（name 含 "/"）。
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			return nil, fmt.Errorf("parse group version %q: %w", list.GroupVersion, err)
		}
		for _, r := range list.APIResources {
			if !slices.Contains(r.Verbs, "list") {
				continue
			}
			category, ok := builtinCategory[schema.GroupResource{Group: gv.Group, Resource: r.Name}]
			if !ok {
				category = CategoryCustom
			}
			result.Types = append(result.Types, ResourceType{
				Group: gv.Group, Version: gv.Version, Resource: r.Name, Kind: r.Kind,
				Namespaced: r.Namespaced, Verbs: []string(r.Verbs), Category: category,
			})
		}
	}

	rank := func(t ResourceType) int {
		if i, ok := builtinRank[schema.GroupResource{Group: t.Group, Resource: t.Resource}]; ok {
			return i
		}
		return len(builtinCategories)
	}
	sort.SliceStable(result.Types, func(i, j int) bool {
		a, b := result.Types[i], result.Types[j]
		if ca, cb := slices.Index(categoryOrder, a.Category), slices.Index(categoryOrder, b.Category); ca != cb {
			return ca < cb
		}
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Kind < b.Kind
	})
	return result, nil
}

// ListResources 以服务端 Table 形式分批列出资源并合并：只保留 priority 0 列（即 kubectl get 默认列），
// 跨命名空间列出命名空间级资源时在名称后插入命名空间列。
func ListResources(ctx context.Context, conn *Conn, q ListQuery) (*ResourceList, error) {
	c, err := newAPIClient(conn)
	if err != nil {
		return nil, err
	}
	include := metav1.IncludeMetadata
	if q.IncludeObject || q.WithPhase {
		include = metav1.IncludeObject
	}

	result := &ResourceList{Columns: []TableColumn{}, Rows: []ResourceRow{}}
	var keep []int
	columnCount := 0
	continueToken := ""
	for page := 0; ; page++ {
		raw, err := c.listPage(ctx, q, tableAccept, listChunkSize, continueToken, map[string]string{"includeObject": string(include)})
		if err != nil {
			return nil, err
		}
		var table metav1.Table
		if err := json.Unmarshal(raw, &table); err != nil {
			return nil, fmt.Errorf("decode table for %s: %w", q.Resource, err)
		}
		if table.Kind != "Table" {
			return nil, fmt.Errorf("server returned %q instead of a Table for %s", table.Kind, q.Resource)
		}
		if page == 0 {
			columnCount = len(table.ColumnDefinitions)
			for i, col := range table.ColumnDefinitions {
				if col.Priority == 0 {
					keep = append(keep, i)
					result.Columns = append(result.Columns, TableColumn{Name: col.Name, Type: col.Type, Format: col.Format})
				}
			}
		}
		for _, tr := range table.Rows {
			if len(tr.Cells) != columnCount {
				return nil, fmt.Errorf("%s table row has %d cells for %d columns", q.Resource, len(tr.Cells), columnCount)
			}
			row, err := tableRow(tr, keep, q.IncludeObject, q.WithPhase)
			if err != nil {
				return nil, fmt.Errorf("decode %s row: %w", q.Resource, err)
			}
			result.Rows = append(result.Rows, row)
		}
		if table.Continue == "" {
			break
		}
		continueToken = table.Continue
	}

	// 集群级类型的行没有命名空间；同一类型的行作用域一致，有一行带命名空间即为命名空间级。
	if q.Namespace == "" && slices.ContainsFunc(result.Rows, func(r ResourceRow) bool { return r.Namespace != "" }) {
		result.Columns = slices.Insert(result.Columns, 1, TableColumn{Name: "Namespace", Type: "string"})
		for i := range result.Rows {
			result.Rows[i].Cells = slices.Insert(result.Rows[i].Cells, 1, any(result.Rows[i].Namespace))
		}
	}
	return result, nil
}

func tableRow(tr metav1.TableRow, keep []int, includeObject, withPhase bool) (ResourceRow, error) {
	var meta metav1.PartialObjectMetadata
	if err := json.Unmarshal(tr.Object.Raw, &meta); err != nil {
		return ResourceRow{}, err
	}
	row := ResourceRow{
		Name:              meta.Name,
		Namespace:         meta.Namespace,
		UID:               string(meta.UID),
		CreationTimestamp: formatTime(meta.CreationTimestamp.Time),
		Labels:            meta.Labels,
		OwnerReferences:   make([]OwnerReference, 0, len(meta.OwnerReferences)),
		Cells:             make([]any, 0, len(keep)),
	}
	if row.Labels == nil {
		row.Labels = map[string]string{}
	}
	for _, ref := range meta.OwnerReferences {
		row.OwnerReferences = append(row.OwnerReferences, OwnerReference{
			APIVersion: ref.APIVersion, Kind: ref.Kind, Name: ref.Name,
			Controller: ref.Controller != nil && *ref.Controller,
		})
	}
	for _, i := range keep {
		row.Cells = append(row.Cells, tr.Cells[i])
	}
	if includeObject || withPhase {
		obj, err := decodeObject(tr.Object.Raw)
		if err != nil {
			return ResourceRow{}, err
		}
		if withPhase {
			status, _ := obj["status"].(map[string]any)
			row.Phase, _ = status["phase"].(string)
		}
		if includeObject {
			row.Object = obj
		}
	}
	return row, nil
}

// CountResources 先取 limit=1 的元数据列表，有 remainingItemCount 即得总数；
// 没有（例如带选择器）时沿 continue 分批数完。失败只影响这一个计数。
func CountResources(ctx context.Context, conn *Conn, q ListQuery) (*ResourceCount, error) {
	c, err := newAPIClient(conn)
	if err != nil {
		return nil, err
	}
	var count int64
	limit := int64(1)
	continueToken := ""
	for {
		raw, err := c.listPage(ctx, q, metadataListAccept, limit, continueToken, nil)
		if err != nil {
			return nil, err
		}
		var list metav1.PartialObjectMetadataList
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decode %s list: %w", q.Resource, err)
		}
		count += int64(len(list.Items))
		if list.Continue == "" {
			return &ResourceCount{Count: count}, nil
		}
		if list.RemainingItemCount != nil {
			return &ResourceCount{Count: count + *list.RemainingItemCount}, nil
		}
		continueToken = list.Continue
		limit = listChunkSize
	}
}

// GetResource 读取单个资源，返回去掉 managedFields 的对象与 YAML；资源不存在时 NotFound=true 而非报错。
func GetResource(ctx context.Context, conn *Conn, ref ResourceRef) (*ResourceObject, error) {
	c, err := newAPIClient(conn)
	if err != nil {
		return nil, err
	}
	raw, err := body(ctx, c.get(ref.Group, ref.Version, ref.Resource, ref.Namespace).
		Name(ref.Name).
		SetHeader("Accept", "application/json"))
	if apierrors.IsNotFound(err) {
		return &ResourceObject{NotFound: true}, nil
	}
	if err != nil {
		return nil, err
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s %s: %w", ref.Resource, ref.Name, err)
	}
	yamlData, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal %s %s to YAML: %w", ref.Resource, ref.Name, err)
	}
	return &ResourceObject{Object: obj, YAML: string(yamlData)}, nil
}

// decodeObject 解出对象并去掉 metadata.managedFields；数字保持原样（json.Number），避免大整数失真。
func decodeObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	if meta, ok := obj["metadata"].(map[string]any); ok {
		delete(meta, "managedFields")
	}
	return obj, nil
}

// ListEvents 分批列出 core/v1 事件，可按对象（类型 + 名称）与告警过滤，按最近发生时间倒序。
func ListEvents(ctx context.Context, conn *Conn, q EventQuery) (*EventList, error) {
	c, err := newAPIClient(conn)
	if err != nil {
		return nil, err
	}
	var selectors []string
	if q.InvolvedKind != "" {
		selectors = append(selectors, "involvedObject.kind="+q.InvolvedKind)
	}
	if q.InvolvedName != "" {
		selectors = append(selectors, "involvedObject.name="+q.InvolvedName)
	}
	if q.WarningOnly {
		selectors = append(selectors, "type="+corev1.EventTypeWarning)
	}
	list := ListQuery{Version: "v1", Resource: "events", Namespace: q.Namespace, FieldSelector: strings.Join(selectors, ",")}

	type timedEvent struct {
		event Event
		last  time.Time
	}
	var timed []timedEvent
	continueToken := ""
	for {
		raw, err := c.listPage(ctx, list, "application/json", listChunkSize, continueToken, nil)
		if err != nil {
			return nil, err
		}
		var page corev1.EventList
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decode events: %w", err)
		}
		for _, e := range page.Items {
			last := eventLastTime(e)
			count := e.Count
			if e.Series != nil {
				count = e.Series.Count
			}
			timed = append(timed, timedEvent{last: last, event: Event{
				Type: e.Type, Reason: e.Reason, Message: e.Message, Count: count, LastTimestamp: formatTime(last),
				InvolvedObject: EventObject{
					APIVersion: e.InvolvedObject.APIVersion, Kind: e.InvolvedObject.Kind,
					Namespace: e.InvolvedObject.Namespace, Name: e.InvolvedObject.Name, UID: string(e.InvolvedObject.UID),
				},
			}})
		}
		if page.Continue == "" {
			break
		}
		continueToken = page.Continue
	}

	sort.SliceStable(timed, func(i, j int) bool { return timed[i].last.After(timed[j].last) })
	result := &EventList{Events: make([]Event, 0, len(timed))}
	for _, t := range timed {
		result.Events = append(result.Events, t.event)
	}
	return result, nil
}

// eventLastTime 取事件最近一次发生的时间：经 events.k8s.io 写入的新式事件没有 lastTimestamp，
// 时间在 series.lastObservedTime（重复发生）或 eventTime（单次）里。
func eventLastTime(e corev1.Event) time.Time {
	switch {
	case e.Series != nil && !e.Series.LastObservedTime.IsZero():
		return e.Series.LastObservedTime.Time
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	default:
		return e.CreationTimestamp.Time
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
