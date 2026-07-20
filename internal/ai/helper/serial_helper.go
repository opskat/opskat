package helper

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/serial_svc"
)

type serialManagerKeyType struct{}

// WithSerialManager 将串口管理器注入 context
func WithSerialManager(ctx context.Context, mgr serial_svc.CommandManager) context.Context {
	return context.WithValue(ctx, serialManagerKeyType{}, mgr)
}

func getSerialManager(ctx context.Context) serial_svc.CommandManager {
	if mgr, ok := ctx.Value(serialManagerKeyType{}).(serial_svc.CommandManager); ok {
		return mgr
	}

	return nil
}

func HandleRunSerialCommand(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	command := aictx.ArgString(args, "command")
	if assetID == 0 {
		return "", fmt.Errorf("missing required parameter: asset_id")
	}
	if command == "" {
		return "", fmt.Errorf("missing required parameter: command")
	}

	mgr := getSerialManager(ctx)
	if mgr == nil {
		return "", fmt.Errorf("serial manager not available")
	}
	if _, ok := mgr.GetSessionByAssetID(assetID); !ok {
		return "", fmt.Errorf("no active serial session for asset %d — please connect the serial port first", assetID)
	}

	// 权限检查
	if checker := permission.GetPolicyChecker(ctx); checker != nil {
		result := checker.CheckForAsset(ctx, assetID, asset_entity.AssetTypeSerial, command)
		aictx.RecordDecision(ctx, result)
		if result.Decision != aictx.Allow {
			return result.Message, nil
		}
	}

	return ExecSerialOnAsset(ctx, &asset_entity.Asset{ID: assetID}, command, "")
}

// ExecSerialOnAsset 是不含权限检查的纯执行入口，供统一 exec 使用。
// HandleRunSerialCommand 保留“检查 + 调用本函数”的形态，两条路径共用同一执行体。
// scope 对串口无意义，忽略。
func ExecSerialOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	mgr := getSerialManager(ctx)
	if mgr == nil {
		return "", fmt.Errorf("serial manager not available")
	}

	sess, ok := mgr.GetSessionByAssetID(asset.ID)
	if !ok {
		return "", fmt.Errorf("no active serial session for asset %d — please connect the serial port first", asset.ID)
	}

	output, err := sess.ExecCommand(command, 2*time.Second, 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("serial command failed: %w", err)
	}
	if output == "" {
		return "(no output)", nil
	}

	return output, nil
}
