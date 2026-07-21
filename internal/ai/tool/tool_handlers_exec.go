package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"

	"github.com/pkg/sftp"
)

// checkFileTransfer 在真正建连之前过一次 cp 审批。审批主体是远端路径——grant 按资产
// 存，本地路径不属于任何资产，塞进 pattern 无法匹配；方向与本地路径进 detail 供展示。
//
// checker 为 nil 只在 opsctl 已完成审批并通过 WithPreapproved 标记上下文时
// 合法；其余缺少 checker 的路径 fail-closed。
func checkFileTransfer(ctx context.Context, assetID int64, remotePath, detail string) error {
	checker, err := permission.RequireCheckerOrPreapproved(ctx)
	if err != nil {
		return err
	}
	if checker == nil {
		return nil
	}
	result := checker.CheckForAsset(ctx, assetID, permission.GrantToolCp, remotePath, detail)
	aictx.RecordDecision(ctx, result)
	if result.Decision != aictx.Allow {
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

func handleRequestGrant(ctx context.Context, args map[string]any) (string, error) {
	itemsJSON := aictx.ArgString(args, "items")
	reason := aictx.ArgString(args, "reason")
	if itemsJSON == "" {
		return "", fmt.Errorf("missing required parameter: items")
	}

	var rawItems []struct {
		AssetID         int64  `json:"asset_id"`
		CommandPatterns string `json:"command_patterns"`
	}
	if err := json.Unmarshal([]byte(itemsJSON), &rawItems); err != nil {
		return "", fmt.Errorf("invalid items JSON: %w", err)
	}
	if len(rawItems) == 0 {
		return "", fmt.Errorf("items must not be empty")
	}

	var grantItems []permission.GrantItem
	for _, raw := range rawItems {
		if raw.AssetID == 0 {
			return "", fmt.Errorf("each item must have a non-zero asset_id")
		}
		var patterns []string
		for _, line := range strings.Split(raw.CommandPatterns, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				patterns = append(patterns, line)
			}
		}
		if len(patterns) == 0 {
			continue
		}
		grantItems = append(grantItems, permission.GrantItem{AssetID: raw.AssetID, Patterns: patterns})
	}
	if len(grantItems) == 0 {
		return "", fmt.Errorf("no valid command patterns provided")
	}

	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}

	result := checker.SubmitGrantMulti(ctx, grantItems, reason)
	aictx.RecordDecision(ctx, result)
	return result.Message, nil
}

func handleUploadFile(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	localPath := aictx.ArgString(args, "local_path")
	remotePath := aictx.ArgString(args, "remote_path")
	if assetID == 0 || localPath == "" || remotePath == "" {
		return "", fmt.Errorf("missing required parameters: asset_id, local_path, remote_path")
	}

	if err := checkFileTransfer(ctx, assetID, remotePath, fmt.Sprintf("upload %s → %s", localPath, remotePath)); err != nil {
		return "", err
	}

	err := helper.ExecuteWithSFTP(ctx, assetID, func(client *sftp.Client) error {
		srcFile, err := os.Open(localPath) //nolint:gosec
		if err != nil {
			return fmt.Errorf("failed to open local file: %w", err)
		}
		defer func() {
			if err := srcFile.Close(); err != nil && !helper.IsExpectedCloseErr(err) {
				logger.Default().Warn("close local file", zap.String("path", localPath), zap.Error(err))
			}
		}()

		dstFile, err := client.Create(remotePath)
		if err != nil {
			return fmt.Errorf("failed to create remote file: %w", err)
		}
		defer func() {
			if err := dstFile.Close(); err != nil && !helper.IsExpectedCloseErr(err) {
				logger.Default().Warn("close remote file", zap.String("path", remotePath), zap.Error(err))
			}
		}()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"message":"file uploaded successfully","remote_path":"%s"}`, remotePath), nil
}

func handleDownloadFile(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	remotePath := aictx.ArgString(args, "remote_path")
	localPath := aictx.ArgString(args, "local_path")
	if assetID == 0 || remotePath == "" || localPath == "" {
		return "", fmt.Errorf("missing required parameters: asset_id, remote_path, local_path")
	}

	if err := checkFileTransfer(ctx, assetID, remotePath, fmt.Sprintf("download %s → %s", remotePath, localPath)); err != nil {
		return "", err
	}

	err := helper.ExecuteWithSFTP(ctx, assetID, func(client *sftp.Client) error {
		srcFile, err := client.Open(remotePath)
		if err != nil {
			return fmt.Errorf("failed to open remote file: %w", err)
		}
		defer func() {
			if err := srcFile.Close(); err != nil && !helper.IsExpectedCloseErr(err) {
				logger.Default().Warn("close remote file", zap.String("path", remotePath), zap.Error(err))
			}
		}()

		dstFile, err := os.Create(localPath) //nolint:gosec
		if err != nil {
			return fmt.Errorf("failed to create local file: %w", err)
		}
		defer func() {
			if err := dstFile.Close(); err != nil && !helper.IsExpectedCloseErr(err) {
				logger.Default().Warn("close local file", zap.String("path", localPath), zap.Error(err))
			}
		}()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"message":"file downloaded successfully","local_path":"%s"}`, localPath), nil
}
