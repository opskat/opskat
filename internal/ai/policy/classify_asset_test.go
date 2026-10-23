package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// splittingDriver 是声明了语句切分分类的假驱动：默认把整段 SQL 当一条 MERGE。
type splittingDriver struct {
	*dbdrivertest.Fake
	err    error
	output []dbdriver.Statement
}

func (d *splittingDriver) ClassifyStatements(_ context.Context, sql string) ([]dbdriver.Statement, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.output != nil {
		return d.output, nil
	}
	return []dbdriver.Statement{{Start: 0, End: len(sql), Text: sql, Type: "MERGE"}}, nil
}

func registerSplitting(t *testing.T, id string, declare bool) *splittingDriver {
	t.Helper()
	f := dbdrivertest.NewFake()
	f.Desc.ID = dbdriver.DriverID(id)
	f.Desc.Aliases = nil
	if declare {
		f.Desc.Capabilities = append(f.Desc.Capabilities, dbdriver.CapClassifyStatements)
	}
	d := &splittingDriver{Fake: f}
	dbdrivertest.Register(t, d)
	return d
}

func dbAsset(t *testing.T, driver string) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, a.SetDatabaseConfig(&asset_entity.DatabaseConfig{Driver: asset_entity.DatabaseDriver(driver)}))
	return a
}

func TestClassifyForAssetUsesTheDriversStatements(t *testing.T) {
	d := registerSplitting(t, "splitdb", true)
	d.output = []dbdriver.Statement{
		{Start: 0, End: 5, Text: "MERGE", Type: "MERGE"},
		{Start: 7, End: 13, Text: "DELETE", Type: "DELETE", Dangerous: true, Reason: "no_where_delete"},
	}
	stmts, err := ClassifyForAsset(context.Background(), dbAsset(t, "splitdb"), "MERGE; DELETE")
	require.NoError(t, err)
	assert.Equal(t, []StatementInfo{
		{Type: "MERGE", Raw: "MERGE"},
		{Type: "DELETE", Raw: "DELETE", Dangerous: true, Reason: "no_where_delete"},
	}, stmts)
}

func TestClassifyForAssetKeepsTiDBWhenDriverDoesNotDeclare(t *testing.T) {
	registerSplitting(t, "plaindb", false)
	for name, asset := range map[string]*asset_entity.Asset{
		"undeclared driver": dbAsset(t, "plaindb"),
		"no asset":          nil,
		"no driver set":     dbAsset(t, ""),
	} {
		stmts, err := ClassifyForAsset(context.Background(), asset, "SELECT 1; DELETE FROM t")
		require.NoError(t, err, name)
		require.Len(t, stmts, 2, name)
		assert.Equal(t, "SELECT", stmts[0].Type, name)
		assert.Equal(t, "no_where_delete", stmts[1].Reason, name)
	}
}

func TestClassifyForAssetErrors(t *testing.T) {
	d := registerSplitting(t, "splitdb", true)

	t.Run("unregistered driver is unavailable", func(t *testing.T) {
		_, err := ClassifyForAsset(context.Background(), dbAsset(t, "gone"), "SELECT 1")
		require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	})
	t.Run("driver error keeps its reason", func(t *testing.T) {
		d.err = errors.New("unterminated quote at byte 7")
		defer func() { d.err = nil }()
		_, err := ClassifyForAsset(context.Background(), dbAsset(t, "splitdb"), "SELECT 'x")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unterminated quote at byte 7")
		assert.NotErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	})
	t.Run("driver reporting nothing is not an allow", func(t *testing.T) {
		d.output = []dbdriver.Statement{}
		defer func() { d.output = nil }()
		_, err := ClassifyForAsset(context.Background(), dbAsset(t, "splitdb"), "whatever")
		require.Error(t, err)
	})
}

func TestClassifyDenyMessageDistinguishesUnavailableDriver(t *testing.T) {
	_, err := ClassifyForAsset(context.Background(), dbAsset(t, "gone"), "SELECT 1")
	msg := ClassifyDenyMessage(context.Background(), err)
	assert.Contains(t, msg, "unavailable")
	assert.Contains(t, msg, "gone")

	msg = ClassifyDenyMessage(context.Background(), errors.New("boom"))
	assert.Contains(t, msg, "boom")
	assert.NotContains(t, msg, "unavailable")
}

func TestPolicyTestUsesTheGivenAssetsDriver(t *testing.T) {
	registerSplitting(t, "splitdb", true)
	ctx := context.Background()
	qp := &asset_entity.QueryPolicy{AllowTypes: []string{"SELECT"}}

	// MERGE 不在白名单 => 需要确认；"*" 则放行。
	out := testQueryPolicyForAsset(ctx, dbAsset(t, "splitdb"), qp, nil, "MERGE INTO t")
	assert.Equal(t, aictx.NeedConfirm, out.Decision)
	out = testQueryPolicyForAsset(ctx, dbAsset(t, "splitdb"), &asset_entity.QueryPolicy{AllowTypes: []string{"*"}}, nil, "MERGE INTO t")
	assert.Equal(t, aictx.Allow, out.Decision)
	// 只有组、没有资产：仍走 TiDB，TiDB 解析不了 => 拒绝。
	out = testQueryPolicyForAsset(ctx, nil, qp, nil, "MERGE INTO t")
	assert.Equal(t, aictx.Deny, out.Decision)
	// 驱动不可用 => 拒绝并点名。
	out = testQueryPolicyForAsset(ctx, dbAsset(t, "gone"), qp, nil, "SELECT 1")
	assert.Equal(t, aictx.Deny, out.Decision)
	assert.Contains(t, out.Message, "unavailable")
}
