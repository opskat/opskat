package audit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func testAsset(t *testing.T) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{ID: 7, Name: "web-1", Type: asset_entity.AssetTypeSSH, GroupID: 3}
	require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{
		Host:     "10.0.0.1",
		Port:     22,
		Username: "root",
		Password: "s3cr3t-ciphertext",
	}))
	return a
}

// TestWriteAssetChange_WritesDesktopRow 钉住桌面 UI 路径的资产增删改会落审计行。
//
// audit_logs.source 一直定义着 "desktop"，但此前只有 external_edit_svc（外部编辑器
// 会话）在写；桌面资产 CRUD 一条都不写。结果是同一件事——新增/改/删一台机器——
// 从 AI 或 opsctl 做有审计，从界面上做没有，事后根本对不出"谁把这台机器删了"。
func TestWriteAssetChange_WritesDesktopRow(t *testing.T) {
	repo := setupAuditRepo(t)
	ctx := aictx.WithAuditSource(context.Background(), "desktop")

	WriteAssetChange(ctx, ActionDeleteAsset, testAsset(t), nil)

	require.Len(t, repo.logs, 1)
	got := repo.logs[0]
	assert.Equal(t, "desktop", got.Source)
	assert.Equal(t, ActionDeleteAsset, got.ToolName)
	assert.Equal(t, int64(7), got.AssetID)
	assert.Equal(t, "web-1", got.AssetName)
	assert.Equal(t, 1, got.Success)
	assert.Empty(t, got.Error)
}

// TestWriteAssetChange_RequestOmitsCredentials 钉住审计请求体只带白名单字段。
//
// asset_entity.Asset.Config 是整个连接配置的 JSON，里面有 password / credential_id /
// 代理链口令。审计表要能回答"改了哪台机器"，不需要、也绝不能存这些——审计库本身
// 不是加密存储，AI 面板还会把审计行展示出来。
func TestWriteAssetChange_RequestOmitsCredentials(t *testing.T) {
	repo := setupAuditRepo(t)

	WriteAssetChange(context.Background(), ActionUpdateAsset, testAsset(t), nil)

	require.Len(t, repo.logs, 1)
	request := repo.logs[0].Request
	assert.NotContains(t, request, "s3cr3t-ciphertext")
	assert.NotContains(t, strings.ToLower(request), "password")
	// 白名单字段仍然要在，否则审计行看不出改的是谁。
	assert.Contains(t, request, `"name":"web-1"`)
	assert.Contains(t, request, `"type":"ssh"`)
	assert.Contains(t, request, `"group_id":3`)
}

// TestWriteAssetChange_FailureRecordsError 钉住失败的操作也要落行：
// 一次被拒绝或报错的删除同样是"有人试过"，只写成功等于把失败尝试从审计里抹掉。
func TestWriteAssetChange_FailureRecordsError(t *testing.T) {
	repo := setupAuditRepo(t)

	WriteAssetChange(context.Background(), ActionAddAsset, testAsset(t), errors.New("name already exists"))

	require.Len(t, repo.logs, 1)
	assert.Equal(t, 0, repo.logs[0].Success)
	assert.Contains(t, repo.logs[0].Error, "name already exists")
}
