package k8s

import (
	"context"
	"fmt"

	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"

	"go.uber.org/zap"
)

// K8sUpdateRequest 是 UpdateK8sResource 的请求：YAML 里的 metadata.resourceVersion 即读取时的版本。
type K8sUpdateRequest struct {
	Ref  K8sResourceRef `json:"ref"`
	YAML string         `json:"yaml"`
}

// K8sApplyRequest 是 ApplyK8sYAML 的请求；DefaultNamespace 为界面当前选择的命名空间，空表示「全部命名空间」。
type K8sApplyRequest struct {
	AssetID          int64  `json:"assetId"`
	YAML             string `json:"yaml"`
	DefaultNamespace string `json:"defaultNamespace"`
}

// UpdateK8sResource 以读取时的版本号整体更新资源；他人改过则返回 conflict=true 与集群给出的原因。
func (k *K8s) UpdateK8sResource(req K8sUpdateRequest) (string, error) {
	if err := req.Ref.validate(); err != nil {
		return "", fmt.Errorf("update K8S resource: %w", err)
	}
	ref := req.Ref
	target := []zap.Field{
		zap.String("resource", ref.Resource), zap.String("namespace", ref.Namespace), zap.String("name", ref.Name),
	}
	return k.runK8sWrite(ref.AssetID, "update K8S resource", target, func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.UpdateResource(ctx, c.kubeconfig, k8spkg.ResourceRef{
			Group: ref.Group, Version: ref.Version, Resource: ref.Resource, Namespace: ref.Namespace, Name: ref.Name,
		}, req.YAML, c.opts...)
	})
}

// ApplyK8sYAML 对多文档 YAML 逐个做服务端 apply，返回逐文档的结果；某个文档失败不影响其他文档。
func (k *K8s) ApplyK8sYAML(req K8sApplyRequest) (string, error) {
	if req.DefaultNamespace != "" {
		if err := validateSegment("namespace", req.DefaultNamespace); err != nil {
			return "", fmt.Errorf("apply K8S YAML: %w", err)
		}
	}
	target := []zap.Field{zap.String("selectedNamespace", req.DefaultNamespace), zap.Int("yamlBytes", len(req.YAML))}
	return k.runK8sWrite(req.AssetID, "apply K8S YAML", target, func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.ApplyYAML(ctx, c.kubeconfig, req.YAML, applyNamespace(req.DefaultNamespace, c.namespace), c.opts...)
	})
}

// applyNamespace 决定未写命名空间的文档落到哪里：选择了命名空间用它，选择「全部命名空间」时用资产配置的命名空间。
func applyNamespace(selected, configured string) string {
	if selected != "" {
		return selected
	}
	return configured
}

// K8sScaleRequest 是 ScaleK8sResource 的请求。
type K8sScaleRequest struct {
	Ref      K8sResourceRef `json:"ref"`
	Replicas int32          `json:"replicas"`
}

func refTarget(ref K8sResourceRef) []zap.Field {
	return []zap.Field{zap.String("resource", ref.Resource), zap.String("namespace", ref.Namespace), zap.String("name", ref.Name)}
}

func (r K8sResourceRef) pkgRef() k8spkg.ResourceRef {
	return k8spkg.ResourceRef{Group: r.Group, Version: r.Version, Resource: r.Resource, Namespace: r.Namespace, Name: r.Name}
}

// DeleteK8sResource 删除资源（Background 级联）。
func (k *K8s) DeleteK8sResource(ref K8sResourceRef) (string, error) {
	if err := ref.validate(); err != nil {
		return "", fmt.Errorf("delete K8S resource: %w", err)
	}
	return k.runK8sWrite(ref.AssetID, "delete K8S resource", refTarget(ref), func(ctx context.Context, c *k8sCallContext) (any, error) {
		return struct{}{}, k8spkg.DeleteResource(ctx, c.kubeconfig, ref.pkgRef(), c.opts...)
	})
}

// ScaleK8sResource 通过 scale 子资源设置副本数。
func (k *K8s) ScaleK8sResource(req K8sScaleRequest) (string, error) {
	if err := req.Ref.validate(); err != nil {
		return "", fmt.Errorf("scale K8S resource: %w", err)
	}
	if req.Replicas < 0 {
		return "", fmt.Errorf("scale K8S resource: replicas must be >= 0")
	}
	target := append(refTarget(req.Ref), zap.Int32("replicas", req.Replicas))
	return k.runK8sWrite(req.Ref.AssetID, "scale K8S resource", target, func(ctx context.Context, c *k8sCallContext) (any, error) {
		return struct{}{}, k8spkg.ScaleResource(ctx, c.kubeconfig, req.Ref.pkgRef(), req.Replicas, c.opts...)
	})
}

// RestartK8sWorkload 滚动重启工作负载（等同 kubectl rollout restart）。
func (k *K8s) RestartK8sWorkload(ref K8sResourceRef) (string, error) {
	if err := ref.validate(); err != nil {
		return "", fmt.Errorf("restart K8S workload: %w", err)
	}
	return k.runK8sWrite(ref.AssetID, "restart K8S workload", refTarget(ref), func(ctx context.Context, c *k8sCallContext) (any, error) {
		return struct{}{}, k8spkg.RestartWorkload(ctx, c.kubeconfig, ref.pkgRef(), c.opts...)
	})
}
