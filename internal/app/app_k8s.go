package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/k8s"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

func (a *App) GetK8sClusterInfo(assetID int64) (string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("get asset: %w", err)
	}
	if !asset.IsK8s() {
		return "", fmt.Errorf("asset %d is not a K8S cluster", assetID)
	}

	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get K8S config: %w", err)
	}

	token := cfg.Token
	if token == "" && cfg.Kubeconfig == "" && cfg.ApiServer == "" {
		return "", fmt.Errorf("no kubeconfig or api_server configured for this K8S asset")
	}

	info, err := k8s.GetClusterInfo(ctx, cfg.Kubeconfig, cfg.ApiServer, token)
	if err != nil {
		return "", fmt.Errorf("get K8S cluster info: %w", err)
	}

	result, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("marshal cluster info: %w", err)
	}
	return string(result), nil
}

func (a *App) GetK8sNamespaceResources(assetID int64, namespace string) (string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("get asset: %w", err)
	}
	if !asset.IsK8s() {
		return "", fmt.Errorf("asset %d is not a K8S cluster", assetID)
	}

	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get K8S config: %w", err)
	}

	token := cfg.Token
	if token == "" && cfg.Kubeconfig == "" && cfg.ApiServer == "" {
		return "", fmt.Errorf("no kubeconfig or api_server configured for this K8S asset")
	}

	res, err := k8s.GetNamespaceResources(ctx, cfg.Kubeconfig, cfg.ApiServer, token, namespace)
	if err != nil {
		return "", fmt.Errorf("get K8S namespace resources: %w", err)
	}

	result, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("marshal namespace resources: %w", err)
	}
	return string(result), nil
}

func (a *App) GetK8sNamespacePods(assetID int64, namespace string) (string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("get asset: %w", err)
	}
	if !asset.IsK8s() {
		return "", fmt.Errorf("asset %d is not a K8S cluster", assetID)
	}

	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get K8S config: %w", err)
	}

	token := cfg.Token
	if token == "" && cfg.Kubeconfig == "" && cfg.ApiServer == "" {
		return "", fmt.Errorf("no kubeconfig or api_server configured for this K8S asset")
	}

	pods, err := k8s.GetNamespacePods(ctx, cfg.Kubeconfig, cfg.ApiServer, token, namespace)
	if err != nil {
		return "", fmt.Errorf("get K8S namespace pods: %w", err)
	}

	result, err := json.Marshal(pods)
	if err != nil {
		return "", fmt.Errorf("marshal namespace pods: %w", err)
	}
	return string(result), nil
}

func (a *App) GetK8sPodDetail(assetID int64, namespace, podName string) (string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("get asset: %w", err)
	}
	if !asset.IsK8s() {
		return "", fmt.Errorf("asset %d is not a K8S cluster", assetID)
	}

	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get K8S config: %w", err)
	}

	token := cfg.Token
	if token == "" && cfg.Kubeconfig == "" && cfg.ApiServer == "" {
		return "", fmt.Errorf("no kubeconfig or api_server configured for this K8S asset")
	}

	detail, err := k8s.GetPodDetail(ctx, cfg.Kubeconfig, cfg.ApiServer, token, namespace, podName)
	if err != nil {
		return "", fmt.Errorf("get K8S pod detail: %w", err)
	}

	result, err := json.Marshal(detail)
	if err != nil {
		return "", fmt.Errorf("marshal pod detail: %w", err)
	}
	return string(result), nil
}
