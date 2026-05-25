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
		Prefix:   argEtcdBool(args, "prefix"),
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

// FormatEtcdCommand 把结构化 ExecRequest 还原为策略匹配 / 审计可读的命令字符串。
//
// 规则：
//   - 复合 op（"member_list" / "endpoint_status" 等）还原为 "member list" / "endpoint status"；
//   - 依次追加 key、value 与 --prefix 标志；
//   - 与 audit 提取器、SaveGrantPattern 共用同一份格式，避免策略匹配与审计文本漂移。
func FormatEtcdCommand(req *etcd_svc.ExecRequest) string {
	op := strings.ReplaceAll(req.Op, "_", " ")
	parts := []string{op}
	if req.Key != "" {
		parts = append(parts, req.Key)
	}
	if req.Value != "" {
		parts = append(parts, req.Value)
	}
	if req.Prefix {
		parts = append(parts, "--prefix")
	}
	return strings.Join(parts, " ")
}

// argEtcdBool 提取布尔参数。LLM 可能传 true / "true"，统一处理。
func argEtcdBool(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			return strings.EqualFold(strings.TrimSpace(b), "true")
		}
	}
	return false
}
