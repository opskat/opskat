package audit_svc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/migrations"
)

func seed(t *testing.T, rows []*audit_entity.AuditLog) {
	t.Helper()
	dataDir := t.TempDir()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	db.SetDefault(gdb)
	audit_repo.RegisterAudit(audit_repo.NewAudit())
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	for _, r := range rows {
		require.NoError(t, audit_repo.Audit().Create(context.Background(), r))
	}
}

func rowIDs(logs []*audit_entity.AuditLog) []int64 {
	out := make([]int64, 0, len(logs))
	for _, l := range logs {
		out = append(out, l.ID)
	}
	return out
}

func fixture() []*audit_entity.AuditLog {
	return []*audit_entity.AuditLog{
		{ID: 1, Source: "opsctl", ToolName: "exec", AssetID: 11, Success: 1, Decision: "allow", DecisionSource: aictx.SourcePolicyAllow, SessionID: "s1", Createtime: 100},
		{ID: 2, Source: "ai", ToolName: "exec", AssetID: 12, Success: 0, Decision: "deny", DecisionSource: aictx.SourceExecGateBlocked, SessionID: "s1", Createtime: 200},
		{ID: 3, Source: "ai", ToolName: "exec", AssetID: 12, Success: 0, Decision: "deny", DecisionSource: aictx.SourceExecTypeMismatch, SessionID: "s2", Createtime: 300},
		{ID: 4, Source: "desktop", ToolName: "update_asset", AssetID: 31, Success: 1, Createtime: 400},
		{ID: 5, Source: "ai", ToolName: "grant_submit", AssetID: 11, AssetName: "web-01", Command: "systemctl status *, df *", Success: 1, Decision: "allow", SessionID: "s1", Createtime: 150},
		{ID: 6, Source: "ai", ToolName: "grant_submit", AssetID: 12, AssetName: "web-02", Command: "uptime", Success: 1, Decision: "allow", SessionID: "s2", Createtime: 250},
		{ID: 7, Source: "ai", ToolName: "grant_submit", AssetID: 13, AssetName: "web-03", Command: "", Success: 1, Decision: "allow", SessionID: "s1", Createtime: 260},
		{ID: 8, Source: "ai", ToolName: "batch_exec", Success: 1, Decision: "allow", DecisionSource: "future_source", Createtime: 500},
	}
}

func TestListTranslatesFilters(t *testing.T) {
	seed(t, fixture())
	ctx := context.Background()

	cases := []struct {
		name string
		q    Query
		want []int64
	}{
		{"exec_gate decider covers every exec_* source", Query{Decider: aictx.DeciderExecGate}, []int64{3, 2}},
		{"none decider matches rows without a decision source", Query{Decider: aictx.DeciderNone}, []int64{7, 6, 5, 4}},
		{"result failed", Query{Result: ResultFailed}, []int64{3, 2}},
		{"result success with decision allow", Query{Result: ResultSuccess, Decision: "allow", Decider: aictx.DeciderPolicy}, []int64{1}},
		{"asset ids union with session", Query{AssetIDs: []int64{11, 12}, SessionID: "s1"}, []int64{5, 2, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs, total, err := List(ctx, c.q)
			require.NoError(t, err)
			require.Equal(t, c.want, rowIDs(logs))
			require.EqualValues(t, len(c.want), total)
		})
	}
}

func TestListRejectsUnknownEnumValues(t *testing.T) {
	seed(t, nil)
	for _, q := range []Query{
		{Decider: "robot"},
		{Result: "maybe"},
		{Decision: "perhaps"},
		{Source: "cron"},
	} {
		_, _, err := List(context.Background(), q)
		var invalid *InvalidFilterError
		require.True(t, errors.As(err, &invalid), "query %+v must be rejected, got %v", q, err)
		require.NotEmpty(t, invalid.Allowed)
	}
}

func TestFacetsMapsDecisionSourcesToDeciders(t *testing.T) {
	seed(t, fixture())
	f, err := Facets(context.Background(), 0, 0)
	require.NoError(t, err)
	require.Equal(t, "exec", f.Tools[0])
	require.ElementsMatch(t, []string{"exec", "update_asset", "grant_submit", "batch_exec"}, f.Tools)
	// 按 DeciderGroups 顺序，未归组的来源（future_source）不出现
	require.Equal(t, []string{aictx.DeciderPolicy, aictx.DeciderExecGate, aictx.DeciderNone}, f.Deciders)

	windowed, err := Facets(context.Background(), 150, 250)
	require.NoError(t, err)
	require.Equal(t, []string{aictx.DeciderExecGate, aictx.DeciderNone}, windowed.Deciders)
}

func TestSessionGrantsCoverTheWholeSession(t *testing.T) {
	seed(t, fixture())
	grants, err := SessionGrants(context.Background(), "s1")
	require.NoError(t, err)
	// 只要 s1 的 grant_submit，且跳过空模式；与分页无关
	require.Equal(t, []SessionGrant{
		{AssetID: 11, AssetName: "web-01", Patterns: "systemctl status *, df *", Createtime: 150},
	}, grants)

	none, err := SessionGrants(context.Background(), "nope")
	require.NoError(t, err)
	require.Empty(t, none)
}
