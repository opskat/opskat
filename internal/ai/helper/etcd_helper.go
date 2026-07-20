package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/etcd_svc"
)

// HandleExecEtcd 是 exec_etcd AI 工具的入口。
//
// 策略 / grant / 审批由 permission.CheckForAsset 在 svc 调用前完成；
// svc.Exec 内部已有三态日志与 dispatch，这里不重复记录。
func HandleExecEtcd(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	op := strings.ToLower(strings.TrimSpace(aictx.ArgString(args, "op")))
	if assetID == 0 || op == "" {
		return "", fmt.Errorf("missing required parameters: asset_id, op")
	}

	req := &etcd_svc.ExecRequest{
		AssetID:  assetID,
		Op:       op,
		Key:      aictx.ArgString(args, "key"),
		Value:    aictx.ArgString(args, "value"),
		Prefix:   aictx.ArgBool(args, "prefix"),
		Limit:    aictx.ArgInt64(args, "limit"),
		Revision: aictx.ArgInt64(args, "revision"),
		LeaseID:  aictx.ArgInt64(args, "lease_id"),
		Source:   "ai",
	}
	if ttl := aictx.ArgInt64(args, "ttl"); ttl > 0 {
		if req.Args == nil {
			req.Args = map[string]any{}
		}
		req.Args["ttl"] = ttl
	}

	// 把结构化请求还原成策略匹配 / grant pattern 用的命令字符串。
	// 与 audit extractor 的 formatEtcdCommand 保持等价。
	cmd := FormatEtcdCommand(req)

	if checker := permission.GetPolicyChecker(ctx); checker != nil {
		result := checker.CheckForAsset(ctx, assetID, asset_entity.AssetTypeEtcd, cmd)
		aictx.RecordDecision(ctx, result)
		if result.Decision != aictx.Allow {
			return result.Message, nil
		}
	}

	svc := etcd_svc.New(getSSHPool(ctx))
	result, err := svc.Exec(ctx, req)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		logger.Ctx(ctx).Error("marshal etcd result", zap.Int64("assetID", assetID), zap.String("op", op), zap.Error(err))
		return "", fmt.Errorf("failed to marshal etcd result: %w", err)
	}
	return string(data), nil
}

// FormatEtcdCommand 委托给 etcd_svc.FormatCommand——格式定义与其逆函数 ParseCommand
// 同住一处，避免二者再次漂移。保留本名是因为 helper 侧已有调用方。
func FormatEtcdCommand(req *etcd_svc.ExecRequest) string {
	return etcd_svc.FormatCommand(req)
}
