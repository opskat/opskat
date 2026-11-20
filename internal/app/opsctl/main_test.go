package opsctl

import (
	"context"
	"os"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// posixSSHAssets 让审批测试里随手写的资产 ID 都解析成远端 shell 为 POSIX 的 SSH 资产：
// 审批时「始终允许」的拆法取决于资产的远端 shell（permission.NormalizeGrantPatternsFor）。
// 需要别的资产的测试自己注册 repo 并在结束时恢复。
type posixSSHAssets struct{ asset_repo.AssetRepo }

func (posixSSHAssets) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	return &asset_entity.Asset{ID: id, Type: asset_entity.AssetTypeSSH}, nil
}

func TestMain(m *testing.M) {
	asset_repo.RegisterAsset(posixSSHAssets{})
	os.Exit(m.Run())
}
