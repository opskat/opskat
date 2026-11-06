package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// mergeDriver 是声明了语句切分分类的假驱动：整段 SQL 报成一条驱动自己的 MERGE 语句。
type mergeDriver struct{ *dbdrivertest.Fake }

func (d mergeDriver) ClassifyStatements(_ context.Context, sql string) ([]dbdriver.Statement, error) {
	return []dbdriver.Statement{{Start: 0, End: len(sql), Text: sql, Type: "MERGE"}}, nil
}

func registerMergeDriver(t *testing.T) {
	t.Helper()
	f := dbdrivertest.NewFake()
	f.Desc.ID = "mergedb"
	f.Desc.Aliases = nil
	f.Desc.Capabilities = append(f.Desc.Capabilities, dbdriver.CapClassifyStatements)
	dbdrivertest.Register(t, mergeDriver{f})
}

func driverAsset(t *testing.T, driver string, policy *asset_entity.QueryPolicy) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase, CmdPolicy: mustJSON(policy)}
	require.NoError(t, a.SetDatabaseConfig(&asset_entity.DatabaseConfig{Driver: asset_entity.DatabaseDriver(driver)}))
	return a
}

func TestDatabasePermissionUsesTheAssetsDriverStatements(t *testing.T) {
	registerMergeDriver(t)
	ctx, mockRepo, _ := setupPolicyTest(t)
	const merge = "MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET t.v = s.v"

	// 驱动自己的类型不在白名单：需要确认（TiDB 路径会因解析失败直接拒绝）。
	asset := driverAsset(t, "mergedb", &asset_entity.QueryPolicy{AllowTypes: []string{"SELECT"}})
	mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	assert.Equal(t, aictx.NeedConfirm, CheckPermission(ctx, asset_entity.AssetTypeDatabase, 1, merge).Decision)

	// 白名单含 "*" 放行；deny_types 点名该类型则拒绝。
	asset = driverAsset(t, "mergedb", &asset_entity.QueryPolicy{AllowTypes: []string{"*"}})
	mockRepo.EXPECT().Find(gomock.Any(), int64(2)).Return(asset, nil).AnyTimes()
	assert.Equal(t, aictx.Allow, CheckPermission(ctx, asset_entity.AssetTypeDatabase, 2, merge).Decision)

	asset = driverAsset(t, "mergedb", &asset_entity.QueryPolicy{DenyTypes: []string{"MERGE"}})
	mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(asset, nil).AnyTimes()
	assert.Equal(t, aictx.Deny, CheckPermission(ctx, asset_entity.AssetTypeDatabase, 3, merge).Decision)
}

func TestDatabasePermissionDeniesWhenTheDriverIsUnavailable(t *testing.T) {
	ctx, mockRepo, _ := setupPolicyTest(t)
	asset := driverAsset(t, "gone", &asset_entity.QueryPolicy{AllowTypes: []string{"*"}})
	mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	result := CheckPermission(ctx, asset_entity.AssetTypeDatabase, 1, "SELECT 1")
	assert.Equal(t, aictx.Deny, result.Decision)
	assert.Contains(t, result.Message, "unavailable")
	assert.Contains(t, result.Message, "gone")
}

func TestAppendTypeRulesLandsDriverStatementTypesForAssetsOnly(t *testing.T) {
	registerMergeDriver(t)
	const merge = "MERGE INTO t USING s ON t.id = s.id"

	asset := driverAsset(t, "mergedb", &asset_entity.QueryPolicy{})
	landed, err := AppendTypeRules(asset, asset_entity.AssetTypeDatabase, RuleAllow, []string{merge})
	require.NoError(t, err)
	assert.Equal(t, []LandedRule{{Rule: "MERGE", Broader: true}}, landed)

	// 组目标没有驱动：仍走 TiDB，解析不了就报错且不改策略。
	group := &group_entity.Group{ID: 2, Name: "prod"}
	_, err = AppendTypeRules(group, asset_entity.AssetTypeDatabase, RuleAllow, []string{merge})
	require.Error(t, err)
}
