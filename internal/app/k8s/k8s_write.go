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
	return k.runK8sWrite(req.Ref.AssetID, "update K8S resource", refTarget(req.Ref), func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.UpdateResource(ctx, c.kubeconfig, req.Ref.pkgRef(), req.YAML, c.opts...)
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

// K8sCronJobRequest 指向一个 CronJob。
type K8sCronJobRequest struct {
	AssetID   int64  `json:"assetId"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// K8sCronJobSuspendRequest 是 SetK8sCronJobSuspend 的请求。
type K8sCronJobSuspendRequest struct {
	AssetID   int64  `json:"assetId"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Suspend   bool   `json:"suspend"`
}

// K8sNodeUnschedulableRequest 是 SetK8sNodeUnschedulable 的请求。
type K8sNodeUnschedulableRequest struct {
	AssetID       int64  `json:"assetId"`
	Name          string `json:"name"`
	Unschedulable bool   `json:"unschedulable"`
}

// K8sTriggerResult 是立即运行 CronJob 的结果：新建的 Job 名称。
type K8sTriggerResult struct {
	JobName string `json:"jobName"`
}

func validateNamed(namespace, name string, needNamespace bool) error {
	if needNamespace {
		if namespace == "" {
			return fmt.Errorf("namespace is required")
		}
		if err := validateSegment("namespace", namespace); err != nil {
			return err
		}
	}
	if name == "" {
		return fmt.Errorf("name is required")
	}
	return validateSegment("name", name)
}

// TriggerK8sCronJob 按 CronJob 模板立即新建一个 Job（kubectl create job --from=cronjob/...），返回 {jobName}。
func (k *K8s) TriggerK8sCronJob(req K8sCronJobRequest) (string, error) {
	if err := validateNamed(req.Namespace, req.Name, true); err != nil {
		return "", fmt.Errorf("trigger K8S CronJob: %w", err)
	}
	target := []zap.Field{zap.String("namespace", req.Namespace), zap.String("name", req.Name)}
	return k.runK8sWrite(req.AssetID, "trigger K8S CronJob", target, func(ctx context.Context, c *k8sCallContext) (any, error) {
		jobName, err := k8spkg.TriggerCronJob(ctx, c.kubeconfig, req.Namespace, req.Name, c.opts...)
		if err != nil {
			return nil, err
		}
		return K8sTriggerResult{JobName: jobName}, nil
	})
}

// SetK8sCronJobSuspend 暂停 / 恢复 CronJob 的调度（spec.suspend）。
func (k *K8s) SetK8sCronJobSuspend(req K8sCronJobSuspendRequest) (string, error) {
	if err := validateNamed(req.Namespace, req.Name, true); err != nil {
		return "", fmt.Errorf("set K8S CronJob suspend: %w", err)
	}
	target := []zap.Field{zap.String("namespace", req.Namespace), zap.String("name", req.Name), zap.Bool("suspend", req.Suspend)}
	return k.runK8sWrite(req.AssetID, "set K8S CronJob suspend", target, func(ctx context.Context, c *k8sCallContext) (any, error) {
		return struct{}{}, k8spkg.SetCronJobSuspend(ctx, c.kubeconfig, req.Namespace, req.Name, req.Suspend, c.opts...)
	})
}

// SetK8sNodeUnschedulable 停止 / 恢复节点调度（spec.unschedulable），不驱逐 Pod。
func (k *K8s) SetK8sNodeUnschedulable(req K8sNodeUnschedulableRequest) (string, error) {
	if err := validateNamed("", req.Name, false); err != nil {
		return "", fmt.Errorf("set K8S node unschedulable: %w", err)
	}
	target := []zap.Field{zap.String("name", req.Name), zap.Bool("unschedulable", req.Unschedulable)}
	return k.runK8sWrite(req.AssetID, "set K8S node unschedulable", target, func(ctx context.Context, c *k8sCallContext) (any, error) {
		return struct{}{}, k8spkg.SetNodeUnschedulable(ctx, c.kubeconfig, req.Name, req.Unschedulable, c.opts...)
	})
}
