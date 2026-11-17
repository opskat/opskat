package k8s

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/yaml"
)

// fieldManager 是 OpsKat 写入集群时登记的字段管理者。
const fieldManager = "opskat"

// 应用 YAML 每个文档的结果。
const (
	ApplyCreated = "created"
	ApplyUpdated = "updated"
	ApplyFailed  = "failed"
)

// ErrCodeNamespaceRequired 标记「命名空间级资源没有命名空间可落」的失败，由前端本地化显示。
const ErrCodeNamespaceRequired = "namespaceRequired"

// UpdateResult 是编辑 YAML 的保存结果：版本冲突不是调用失败，而是 Conflict=true 与集群给出的原因。
type UpdateResult struct {
	Conflict bool           `json:"conflict"`
	Message  string         `json:"message"`
	Object   map[string]any `json:"object,omitempty"`
}

// ApplyResult 是应用 YAML 中一个文档的结果；Index 为文档在输入中的序号（从 0 起，纯注释的空文档不计）。
type ApplyResult struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Action    string `json:"action"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// ApplyResults 是整份应用 YAML 的逐文档结果。
type ApplyResults struct {
	Results []ApplyResult `json:"results"`
}

// UpdateResource 以 yamlText 整体更新 ref 指向的资源，随请求带上其中 metadata.resourceVersion：
// 资源在读取之后被他人改过时集群回 409，映射为 Conflict 而不是错误。
func UpdateResource(ctx context.Context, kubeconfig string, ref ResourceRef, yamlText string, opts ...ClientOption) (*UpdateResult, error) {
	obj, err := parseObject([]byte(yamlText))
	if err != nil {
		return nil, err
	}
	if obj.GetResourceVersion() == "" {
		return nil, errors.New("metadata.resourceVersion is required to update a resource")
	}
	config, err := restConfig(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s dynamic client: %w", err)
	}
	gvr := schema.GroupVersionResource{Group: ref.Group, Version: ref.Version, Resource: ref.Resource}
	updated, err := resourceClient(dyn, gvr, ref.Namespace).Update(ctx, obj, metav1.UpdateOptions{FieldManager: fieldManager})
	if apierrors.IsConflict(err) {
		return &UpdateResult{Conflict: true, Message: err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	unstructured.RemoveNestedField(updated.Object, "metadata", "managedFields")
	return &UpdateResult{Object: updated.Object}, nil
}

// ApplyYAML 逐个文档做服务端 apply（字段管理者 opskat，不强制抢占字段）。文档按 `---` 分隔，
// 未写命名空间的命名空间级资源落到 defaultNamespace（空则该文档失败）；某个文档失败不影响其他文档。
func ApplyYAML(ctx context.Context, kubeconfig, yamlText, defaultNamespace string, opts ...ClientOption) (*ApplyResults, error) {
	config, err := restConfig(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s discovery client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s dynamic client: %w", err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))

	docs, err := splitDocuments(yamlText)
	if err != nil {
		return nil, err
	}
	results := make([]ApplyResult, 0, len(docs))
	for i, doc := range docs {
		results = append(results, applyDocument(ctx, dyn, mapper, i, doc, defaultNamespace))
	}
	return &ApplyResults{Results: results}, nil
}

// splitDocuments 按 `---` 切分，丢弃空文档（空白或只有注释）。
func splitDocuments(yamlText string) ([][]byte, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(yamlText)))
	var docs [][]byte
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read YAML documents: %w", err)
		}
		if isBlankDocument(doc) {
			continue
		}
		docs = append(docs, doc)
	}
}

func isBlankDocument(doc []byte) bool {
	for _, line := range strings.Split(string(doc), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && line != "---" {
			return false
		}
	}
	return true
}

func parseObject(raw []byte) (*unstructured.Unstructured, error) {
	jsonData, err := yaml.YAMLToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonData); err != nil {
		return nil, fmt.Errorf("parse object: %w", err)
	}
	return obj, nil
}

func resourceClient(dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace string) dynamic.ResourceInterface {
	if namespace == "" {
		return dyn.Resource(gvr)
	}
	return dyn.Resource(gvr).Namespace(namespace)
}

func applyDocument(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, index int, doc []byte, defaultNamespace string) ApplyResult {
	res := ApplyResult{Index: index, Action: ApplyFailed}
	obj, err := parseObject(doc)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Kind, res.Name, res.Namespace = obj.GetKind(), obj.GetName(), obj.GetNamespace()
	if res.Name == "" {
		res.Error = "metadata.name is required"
		return res
	}
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	namespace := ""
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		namespace = res.Namespace
		if namespace == "" {
			namespace = defaultNamespace
		}
		if namespace == "" {
			res.Error, res.ErrorCode = "namespace not specified", ErrCodeNamespaceRequired
			return res
		}
		obj.SetNamespace(namespace)
		res.Namespace = namespace
	}
	// 客户端读回来的 managedFields 不能再提交（集群会拒绝）。
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")

	client := resourceClient(dyn, mapping.Resource, namespace)
	_, getErr := client.Get(ctx, res.Name, metav1.GetOptions{})
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		res.Error = getErr.Error()
		return res
	}
	payload, err := obj.MarshalJSON()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	force := false
	if _, err := client.Patch(ctx, res.Name, types.ApplyPatchType, payload, metav1.PatchOptions{FieldManager: fieldManager, Force: &force}); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Action = ApplyUpdated
	if getErr != nil {
		res.Action = ApplyCreated
	}
	return res
}

// restartedAtAnnotation 是 kubectl rollout restart 写入 Pod 模板的注解，改动模板即触发滚动更新。
const restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

// dynamicFor 用与其他读写一致的 rest 配置建 dynamic client。
func dynamicFor(kubeconfig string, opts ...ClientOption) (dynamic.Interface, error) {
	config, err := restConfig(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s dynamic client: %w", err)
	}
	return dyn, nil
}

func gvrOf(ref ResourceRef) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: ref.Group, Version: ref.Version, Resource: ref.Resource}
}

// DeleteResource 删除 ref 指向的资源，级联策略为 Background（与 kubectl delete 默认一致：立即返回，依赖对象由 GC 后台清理）。
func DeleteResource(ctx context.Context, kubeconfig string, ref ResourceRef, opts ...ClientOption) error {
	dyn, err := dynamicFor(kubeconfig, opts...)
	if err != nil {
		return err
	}
	policy := metav1.DeletePropagationBackground
	return resourceClient(dyn, gvrOf(ref), ref.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
}

// ScaleResource 通过 scale 子资源把副本数设为 replicas（≥ 0）。
func ScaleResource(ctx context.Context, kubeconfig string, ref ResourceRef, replicas int32, opts ...ClientOption) error {
	if replicas < 0 {
		return fmt.Errorf("replicas must be >= 0, got %d", replicas)
	}
	dyn, err := dynamicFor(kubeconfig, opts...)
	if err != nil {
		return err
	}
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	_, err = resourceClient(dyn, gvrOf(ref), ref.Namespace).Patch(ctx, ref.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}, "scale")
	return err
}

// RestartWorkload 与 kubectl rollout restart 相同：把 Pod 模板注解 restartedAt 改为当前时间。
func RestartWorkload(ctx context.Context, kubeconfig string, ref ResourceRef, opts ...ClientOption) error {
	dyn, err := dynamicFor(kubeconfig, opts...)
	if err != nil {
		return err
	}
	patch := map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]any{restartedAtAnnotation: time.Now().Format(time.RFC3339)},
	}}}}
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal restart patch: %w", err)
	}
	_, err = resourceClient(dyn, gvrOf(ref), ref.Namespace).Patch(ctx, ref.Name, types.StrategicMergePatchType, raw, metav1.PatchOptions{FieldManager: fieldManager})
	return err
}

var (
	cronJobGVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}
	jobGVR     = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	nodeGVR    = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
)

const (
	// instantiateAnnotation 与 kubectl create job --from=cronjob/... 一致，标记 Job 由人工触发。
	instantiateAnnotation = "cronjob.kubernetes.io/instantiate"
	manualJobInfix        = "-manual-"
	manualSuffixLen       = 5
	maxJobNameLen         = 63
)

// manualJobName 生成 <cronjob>-manual-<随机后缀>：cronjob 名按需截断，使整体不超过 63 个字符且截断处不留 '-' / '.'。
func manualJobName(cronJob string) string {
	keep := maxJobNameLen - len(manualJobInfix) - manualSuffixLen
	if len(cronJob) > keep {
		cronJob = strings.TrimRight(cronJob[:keep], "-.")
	}
	return cronJob + manualJobInfix + utilrand.String(manualSuffixLen)
}

// TriggerCronJob 与 kubectl create job --from=cronjob/<name> 一样，按 CronJob 的 jobTemplate 新建一个 Job
// （注解 instantiate=manual，属主为该 CronJob），不改动 CronJob 本身，返回新 Job 的名称。
func TriggerCronJob(ctx context.Context, kubeconfig, namespace, name string, opts ...ClientOption) (string, error) {
	dyn, err := dynamicFor(kubeconfig, opts...)
	if err != nil {
		return "", err
	}
	cron, err := dyn.Resource(cronJobGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	template, _, err := unstructured.NestedMap(cron.Object, "spec", "jobTemplate")
	if err != nil {
		return "", fmt.Errorf("read CronJob jobTemplate: %w", err)
	}
	jobSpec, _ := template["spec"].(map[string]any)
	if jobSpec == nil {
		return "", errors.New("CronJob has no spec.jobTemplate.spec")
	}
	job := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "batch/v1", "kind": "Job", "spec": jobSpec}}
	job.SetName(manualJobName(name))
	job.SetNamespace(namespace)
	tm, _ := template["metadata"].(map[string]any)
	if labels, _, _ := unstructured.NestedStringMap(tm, "labels"); len(labels) > 0 {
		job.SetLabels(labels)
	}
	annotations, _, _ := unstructured.NestedStringMap(tm, "annotations")
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[instantiateAnnotation] = "manual"
	job.SetAnnotations(annotations)
	controller := true
	job.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "batch/v1", Kind: "CronJob", Name: cron.GetName(), UID: cron.GetUID(), Controller: &controller,
	}})
	created, err := dyn.Resource(jobGVR).Namespace(namespace).Create(ctx, job, metav1.CreateOptions{FieldManager: fieldManager})
	if err != nil {
		return "", err
	}
	return created.GetName(), nil
}

func mergePatch(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, patch map[string]any) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	_, err = resourceClient(dyn, gvr, namespace).Patch(ctx, name, types.MergePatchType, raw, metav1.PatchOptions{FieldManager: fieldManager})
	return err
}

// SetCronJobSuspend 切换 CronJob 的 spec.suspend。
func SetCronJobSuspend(ctx context.Context, kubeconfig, namespace, name string, suspend bool, opts ...ClientOption) error {
	dyn, err := dynamicFor(kubeconfig, opts...)
	if err != nil {
		return err
	}
	return mergePatch(ctx, dyn, cronJobGVR, namespace, name, map[string]any{"spec": map[string]any{"suspend": suspend}})
}

// SetNodeUnschedulable 只切换节点的 spec.unschedulable（cordon / uncordon），不驱逐任何 Pod。
func SetNodeUnschedulable(ctx context.Context, kubeconfig, name string, unschedulable bool, opts ...ClientOption) error {
	dyn, err := dynamicFor(kubeconfig, opts...)
	if err != nil {
		return err
	}
	return mergePatch(ctx, dyn, nodeGVR, "", name, map[string]any{"spec": map[string]any{"unschedulable": unschedulable}})
}
