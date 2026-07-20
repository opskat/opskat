package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

func handleExecK8s(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	command := aictx.ArgString(args, "command")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("missing required parameter: command")
	}

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("get asset: %w", err)
	}
	if asset == nil || !asset.IsK8s() {
		return "", fmt.Errorf("asset %d is not a k8s cluster", assetID)
	}

	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get k8s config: %w", err)
	}
	if cfg.Kubeconfig == "" {
		return "", fmt.Errorf("no kubeconfig configured for this k8s asset")
	}

	plan, err := helper.BuildK8sCommandPlan(command, cfg)
	if err != nil {
		return "", err
	}

	if checker := permission.GetPolicyChecker(ctx); checker != nil {
		result := checker.CheckForAsset(ctx, assetID, asset_entity.AssetTypeK8s, plan.EffectiveCommand)
		aictx.RecordDecision(ctx, result)
		if result.Decision != aictx.Allow {
			return result.Message, nil
		}
	}

	return helper.ExecK8sOnAsset(ctx, asset, command, "")
}

func k8sAuditCommandFromArgs(args map[string]any) string {
	command := aictx.ArgString(args, "command")
	if strings.TrimSpace(command) == "" {
		return ""
	}

	var cfg *asset_entity.K8sConfig
	assetID := aictx.ArgInt64(args, "asset_id")
	if assetID > 0 && asset_repo.Asset() != nil {
		if asset, err := asset_repo.Asset().Find(context.Background(), assetID); err == nil && asset != nil && asset.IsK8s() {
			if k8sCfg, cfgErr := asset.GetK8sConfig(); cfgErr == nil {
				cfg = k8sCfg
			}
		}
	}

	plan, err := helper.BuildK8sCommandPlan(command, cfg)
	if err != nil {
		return command
	}
	return plan.EffectiveCommand
}
