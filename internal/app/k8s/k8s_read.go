package k8s

import (
	"context"
	"fmt"
	"strings"

	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"

	"k8s.io/apimachinery/pkg/api/validate/content"
)

// K8sListRequest 是 ListK8sResources / CountK8sResources 的请求；Namespace 为空表示全部命名空间。
type K8sListRequest struct {
	AssetID       int64  `json:"assetId"`
	Group         string `json:"group"`
	Version       string `json:"version"`
	Resource      string `json:"resource"`
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
	FieldSelector string `json:"fieldSelector"`
	IncludeObject bool   `json:"includeObject"`
	WithPhase     bool   `json:"withPhase"`
}

// K8sResourceRef 指向单个资源；集群级资源 Namespace 为空。
type K8sResourceRef struct {
	AssetID   int64  `json:"assetId"`
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// K8sEventsRequest 是 ListK8sEvents 的请求；Namespace 为空表示全部命名空间，InvolvedKind / InvolvedName 非空时只取该对象的事件。
type K8sEventsRequest struct {
	AssetID      int64  `json:"assetId"`
	Namespace    string `json:"namespace"`
	InvolvedKind string `json:"involvedKind"`
	InvolvedName string `json:"involvedName"`
	WarningOnly  bool   `json:"warningOnly"`
}

func (r K8sListRequest) validate() error {
	return validateResourcePath(r.Group, r.Version, r.Resource, r.Namespace)
}

func (r K8sListRequest) query() k8spkg.ListQuery {
	return k8spkg.ListQuery{
		Group: r.Group, Version: r.Version, Resource: r.Resource, Namespace: r.Namespace,
		LabelSelector: r.LabelSelector, FieldSelector: r.FieldSelector, IncludeObject: r.IncludeObject, WithPhase: r.WithPhase,
	}
}

func (r K8sResourceRef) validate() error {
	if err := validateResourcePath(r.Group, r.Version, r.Resource, r.Namespace); err != nil {
		return err
	}
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	return validateSegment("name", r.Name)
}

// validateResourcePath 校验拼进 API 路径的各段：前端传来的值不能越出所指的资源路径。
func validateResourcePath(group, version, resource, namespace string) error {
	if version == "" {
		return fmt.Errorf("version is required")
	}
	if resource == "" {
		return fmt.Errorf("resource is required")
	}
	for _, seg := range []struct{ field, value string }{
		{"group", group}, {"version", version}, {"resource", resource}, {"namespace", namespace},
	} {
		if seg.value == "" {
			continue
		}
		if err := validateSegment(seg.field, seg.value); err != nil {
			return err
		}
	}
	return nil
}

func validateSegment(field, value string) error {
	if msgs := content.IsPathSegmentName(value); len(msgs) > 0 {
		return fmt.Errorf("invalid %s %q: %s", field, value, strings.Join(msgs, "; "))
	}
	return nil
}

// GetK8sClusterVersion 只读集群版本：集群页打开唯一依赖的读取。
func (k *K8s) GetK8sClusterVersion(assetID int64) (string, error) {
	return k.runK8sCall(assetID, "get K8S cluster version", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetClusterVersion(ctx, c.kubeconfig, c.opts...)
	})
}

// GetK8sResourceTypes 列出集群可列出的资源类型（按类别归组），附带发现失败的 API 组。
func (k *K8s) GetK8sResourceTypes(assetID int64) (string, error) {
	return k.runK8sCall(assetID, "get K8S resource types", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetResourceTypes(ctx, c.kubeconfig, c.opts...)
	})
}

// ListK8sResources 以 kubectl get 默认列列出某类资源。
func (k *K8s) ListK8sResources(req K8sListRequest) (string, error) {
	if err := req.validate(); err != nil {
		return "", fmt.Errorf("list K8S resources: %w", err)
	}
	return k.runK8sCall(req.AssetID, "list K8S resources", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.ListResources(ctx, c.kubeconfig, req.query(), c.opts...)
	})
}

// CountK8sResources 统计某类资源数量；每个计数独立失败。
func (k *K8s) CountK8sResources(req K8sListRequest) (string, error) {
	if err := req.validate(); err != nil {
		return "", fmt.Errorf("count K8S resources: %w", err)
	}
	return k.runK8sCall(req.AssetID, "count K8S resources", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.CountResources(ctx, c.kubeconfig, req.query(), c.opts...)
	})
}

// GetK8sResource 读取单个资源（去掉 managedFields）；资源已不存在时返回 notFound。
func (k *K8s) GetK8sResource(ref K8sResourceRef) (string, error) {
	if err := ref.validate(); err != nil {
		return "", fmt.Errorf("get K8S resource: %w", err)
	}
	return k.runK8sCall(ref.AssetID, "get K8S resource", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetResource(ctx, c.kubeconfig, k8spkg.ResourceRef{
			Group: ref.Group, Version: ref.Version, Resource: ref.Resource, Namespace: ref.Namespace, Name: ref.Name,
		}, c.opts...)
	})
}

// ListK8sEvents 列出事件，按最近发生时间倒序。
func (k *K8s) ListK8sEvents(req K8sEventsRequest) (string, error) {
	if req.Namespace != "" {
		if err := validateSegment("namespace", req.Namespace); err != nil {
			return "", fmt.Errorf("list K8S events: %w", err)
		}
	}
	return k.runK8sCall(req.AssetID, "list K8S events", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.ListEvents(ctx, c.kubeconfig, k8spkg.EventQuery{
			Namespace: req.Namespace, InvolvedKind: req.InvolvedKind, InvolvedName: req.InvolvedName, WarningOnly: req.WarningOnly,
		}, c.opts...)
	})
}

// GetK8sClusterHealth 读取集群概览的健康汇总与异常清单；namespace 为空表示全部命名空间。
// 各区块独立失败，失败原因在返回值的 errors 里，不使整个调用失败。
func (k *K8s) GetK8sClusterHealth(assetID int64, namespace string) (string, error) {
	if namespace != "" {
		if err := validateSegment("namespace", namespace); err != nil {
			return "", fmt.Errorf("get K8S cluster health: %w", err)
		}
	}
	return k.runK8sCall(assetID, "get K8S cluster health", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetClusterHealth(ctx, c.kubeconfig, namespace, c.opts...)
	})
}
