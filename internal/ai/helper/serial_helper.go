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
		return "", errNoActiveSerialSession(assetID)
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
		return "", errNoActiveSerialSession(asset.ID)
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

// errNoActiveSerialSession 是三处共用的同一句错误：HandleRunSerialCommand、
// ExecSerialOnAsset，以及为统一 exec 注册的 PrecheckSerialSession。三条路径必须报
// 同一句话——把它散落成三个字面量迟早会漂移。
func errNoActiveSerialSession(assetID int64) error {
	return fmt.Errorf("no active serial session for asset %d — please connect the serial port first", assetID)
}

// PrecheckSerialSession 是给统一 exec 注册的 permission.PrecheckFunc：把
// HandleRunSerialCommand 早就在做的"会话是否存在"检查挪到权限检查之前，让统一 exec
// 对一个没有活跃会话的串口资产，走到跟旧路径一样的失败——不弹审批对话框。
//
// 这正是 canonicalizeK8sCommand（internal/ai/execimpl/register.go）解决的同一类问题：
// 该检查无副作用，必须排在 CheckForAsset（可能弹审批对话框并阻塞等待用户响应）之前，
// 否则用户会先被弹一次审批，批准之后命令才因为没有会话而失败。串口没有可规范化的
// 命令，所以是 precheck 而不是 canonicalize。
func PrecheckSerialSession(ctx context.Context, asset *asset_entity.Asset) error {
	mgr := getSerialManager(ctx)
	if mgr == nil {
		return fmt.Errorf("serial manager not available")
	}
	if _, ok := mgr.GetSessionByAssetID(asset.ID); !ok {
		return errNoActiveSerialSession(asset.ID)
	}
	return nil
}
