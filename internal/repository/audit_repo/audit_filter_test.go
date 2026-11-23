package audit_repo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/migrations"
)

// openFilterRepo 建一个跑过真实迁移的临时库并写入 rows，返回仓储。
func openFilterRepo(t *testing.T, rows []*audit_entity.AuditLog) AuditRepo {
	t.Helper()
	dataDir := t.TempDir()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	db.SetDefault(gdb)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	repo := NewAudit()
	for _, r := range rows {
		require.NoError(t, repo.Create(context.Background(), r))
	}
	return repo
}

func ids(logs []*audit_entity.AuditLog) []int64 {
	out := make([]int64, 0, len(logs))
	for _, l := range logs {
		out = append(out, l.ID)
	}
	return out
}

func filterFixture() []*audit_entity.AuditLog {
	return []*audit_entity.AuditLog{
		{ID: 1, Source: "opsctl", ToolName: "exec", AssetID: 11, AssetName: "web-01", Command: "systemctl status NGINX", Success: 1, Decision: "allow", DecisionSource: "policy_allow", Createtime: 100},
		{ID: 2, Source: "ai", ToolName: "exec", AssetID: 12, AssetName: "web-02", Command: "rm -rf /tmp/100%_done", Success: 0, Decision: "deny", DecisionSource: "policy_deny", Createtime: 200},
		{ID: 3, Source: "ai", ToolName: "exec_sql", AssetID: 21, AssetName: "nginx-db", Command: "SELECT 1", Success: 1, Decision: "allow", DecisionSource: "user_allow", Createtime: 300},
		{ID: 4, Source: "desktop", ToolName: "update_asset", AssetID: 31, AssetName: "cache", Command: "", Success: 1, Decision: "", DecisionSource: "", Createtime: 400},
		{ID: 5, Source: "ai", ToolName: "exec", AssetID: 12, AssetName: "web-02", Command: "rm -rf /tmp/100x_done", Success: 0, Decision: "deny", DecisionSource: "exec_gate_blocked", Createtime: 500},
	}
}

func TestAuditListFilters(t *testing.T) {
	repo := openFilterRepo(t, filterFixture())
	ctx := context.Background()
	yes, no := true, false

	cases := []struct {
		name string
		opts ListOptions
		want []int64
	}{
		{"keyword matches command case-insensitively", ListOptions{Keyword: "nginx"}, []int64{3, 1}},
		{"keyword matches asset name", ListOptions{Keyword: "WEB-02"}, []int64{5, 2}},
		{"keyword percent and underscore are literal", ListOptions{Keyword: "100%_"}, []int64{2}},
		{"tool name", ListOptions{ToolName: "exec_sql"}, []int64{3}},
		{"asset ids are a union", ListOptions{AssetIDs: []int64{11, 21}}, []int64{3, 1}},
		{"success only", ListOptions{Success: &yes}, []int64{4, 3, 1}},
		{"failed only", ListOptions{Success: &no}, []int64{5, 2}},
		{"decision deny", ListOptions{Decision: "deny"}, []int64{5, 2}},
		{"decision sources include empty", ListOptions{DecisionSources: []string{""}}, []int64{4}},
		{"decision sources set", ListOptions{DecisionSources: []string{"policy_allow", "policy_deny"}}, []int64{2, 1}},
		{"combined", ListOptions{Source: "ai", Decision: "deny", DecisionSources: []string{"policy_deny"}, AssetIDs: []int64{12}}, []int64{2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs, total, err := repo.List(ctx, c.opts)
			require.NoError(t, err)
			require.Equal(t, c.want, ids(logs))
			require.EqualValues(t, len(c.want), total)
		})
	}

	t.Run("total counts every match while a page is limited", func(t *testing.T) {
		logs, total, err := repo.List(ctx, ListOptions{Source: "ai", Limit: 1, Offset: 1})
		require.NoError(t, err)
		require.Equal(t, []int64{3}, ids(logs))
		require.EqualValues(t, 3, total)
	})
}

func TestAuditFacets(t *testing.T) {
	rows := filterFixture()
	rows = append(rows, &audit_entity.AuditLog{ID: 6, Source: "ai", ToolName: "exec", DecisionSource: "grant_allow", Createtime: 9000})
	repo := openFilterRepo(t, rows)
	ctx := context.Background()

	f, err := repo.Facets(ctx, 150, 450)
	require.NoError(t, err)
	// 工具按出现次数降序、同数按名称；决策来源去重（含空值）。
	require.Equal(t, []string{"exec", "exec_sql", "update_asset"}, f.ToolNames)
	require.ElementsMatch(t, []string{"policy_deny", "user_allow", ""}, f.DecisionSources)

	all, err := repo.Facets(ctx, 0, 0)
	require.NoError(t, err)
	require.Equal(t, "exec", all.ToolNames[0])
	require.Contains(t, all.DecisionSources, "grant_allow")
}
