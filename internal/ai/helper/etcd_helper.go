package helper

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// marshalEtcdResult serializes an etcd_svc.Exec result to the JSON string returned to
// the model. Used by ExecEtcdOnAsset (the unified exec tool's etcd executor,
// etcd_exec.go).
func marshalEtcdResult(ctx context.Context, assetID int64, op string, result any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		logger.Ctx(ctx).Error("marshal etcd result", zap.Int64("assetID", assetID), zap.String("op", op), zap.Error(err))
		return "", fmt.Errorf("failed to marshal etcd result: %w", err)
	}
	return string(data), nil
}
