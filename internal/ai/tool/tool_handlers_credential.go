package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/service/credential_query_svc"
	"go.uber.org/zap"
)

func handleListCredentials(ctx context.Context, args map[string]any) (string, error) {
	items, err := credential_query_svc.Default().List(ctx, credential_query_svc.ListOptions{
		Type: aictx.ArgString(args, "type"),
	})
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(items)
	if err != nil {
		logger.Ctx(ctx).Error("marshal credential list failed", zap.Error(err))
		return "", fmt.Errorf("failed to marshal credential list: %w", err)
	}
	return string(data), nil
}

func handleGetCredential(ctx context.Context, args map[string]any) (string, error) {
	ref := aictx.ArgString(args, "ref")
	if ref == "" {
		return "", fmt.Errorf("missing required parameter: ref")
	}
	detail, err := credential_query_svc.Default().Get(ctx, ref)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(detail)
	if err != nil {
		logger.Ctx(ctx).Error("marshal credential detail failed", zap.String("ref", ref), zap.Error(err))
		return "", fmt.Errorf("failed to marshal credential detail: %w", err)
	}
	return string(data), nil
}
