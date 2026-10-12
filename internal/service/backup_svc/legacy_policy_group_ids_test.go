package backup_svc

import (
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
)

// v1.1.1 及更早版本导出的备份里，策略的 groups 是数字 ID（内置组为负数）。一次性迁移只改
// 当时库里的行，管不到之后才导入的备份，所以导入必须自己把它们升成现行的字符串 ID——
// 原样落库的策略整条解析不了。
func TestImport_UpgradesLegacyPolicyGroupIDs(t *testing.T) {
	ctx := setupBackupTest(t)

	asset := &asset_entity.Asset{
		ID: 9, Name: "bastion", Type: asset_entity.AssetTypeSSH, GroupID: 4, Status: asset_entity.StatusActive,
		CmdPolicy: `{"allow_list":["*"],"deny_list":null,"groups":[-1,-4]}`,
	}
	require.NoError(t, asset.SetSSHConfig(&asset_entity.SSHConfig{
		Host: "10.0.0.1", Port: 22, Username: "root", AuthType: asset_entity.AuthTypePassword,
	}))
	data := &BackupData{
		Version: "1.0",
		Groups: []*group_entity.Group{{
			ID: 4, Name: "prod",
			CmdPolicy: `{"groups":[-4]}`,
			QryPolicy: `{"groups":[-5,-6]}`,
			RdsPolicy: `{"groups":[-7]}`,
		}},
		Assets: []*asset_entity.Asset{asset},
	}

	_, err := Import(ctx, data, &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
	require.NoError(t, err)

	var gotAsset asset_entity.Asset
	require.NoError(t, db.Ctx(ctx).Where("name = ?", "bastion").First(&gotAsset).Error)
	cmdPolicy, err := gotAsset.GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, cmdPolicy.AllowList)
	assert.Equal(t, []string{"builtin:linux-readonly", "builtin:dangerous-deny"}, cmdPolicy.Groups)

	var gotGroup group_entity.Group
	require.NoError(t, db.Ctx(ctx).Where("name = ?", "prod").First(&gotGroup).Error)
	groupCmd, err := gotGroup.GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin:dangerous-deny"}, groupCmd.Groups)
	groupQuery, err := gotGroup.GetQueryPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin:sql-readonly", "builtin:sql-dangerous-deny"}, groupQuery.Groups)
	groupRedis, err := gotGroup.GetRedisPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin:redis-readonly"}, groupRedis.Groups)
}
