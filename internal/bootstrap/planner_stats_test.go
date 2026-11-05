package bootstrap

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/migrations"
)

type selectRecorder struct {
	gormlogger.Interface
	stmts []string
}

func (r *selectRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	if sql, _ := fc(); strings.HasPrefix(sql, "SELECT") {
		r.stmts = append(r.stmts, sql)
	}
}

// 新装（迁移时 audit_logs 还是空表、ANALYZE 留不下统计）之后审计日志长到 20 万行，
// 下次启动后审计页的时间窗查询仍要按时间窗走索引，而不是把整个索引扫一遍。
func TestInitKeepsAuditWindowQueriesOnIndexRangeAfterTableGrows(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 200k audit rows")
	}
	prev := resolvedDataDir
	t.Cleanup(func() { resolvedDataDir = prev })

	const rows, day, end = 200000, int64(86400), int64(1_800_000_000)
	dataDir := t.TempDir()
	fresh, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(fresh, dataDir))
	freshDB, err := fresh.DB()
	require.NoError(t, err)
	freshDB.SetMaxOpenConns(1)
	require.NoError(t, fresh.Exec("PRAGMA synchronous = OFF").Error)
	require.NoError(t, fresh.Exec(fmt.Sprintf(`INSERT INTO audit_logs (source, tool_name, session_id, createtime)
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < %d)
SELECT CASE i %% 20 WHEN 0 THEN 'opsctl' WHEN 1 THEN 'ui' ELSE 'ai' END, 'exec',
       CASE WHEN i %% 10 = 0 THEN '' ELSE 'sess-' || (i %% 4000) END,
       %d - (%d - i) * (60 * %d) / %d
FROM n`, rows, end, rows, day, rows)).Error)
	require.NoError(t, freshDB.Close())

	require.NoError(t, Init(context.Background(), Options{DataDir: dataDir, MasterKey: "test-master-key"}))
	sqlDB, err := db.Default().DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	gdb := db.Default()
	rec := &selectRecorder{Interface: gormlogger.Discard}
	db.SetDefault(gdb.Session(&gorm.Session{Logger: rec}))
	t.Cleanup(func() { db.SetDefault(gdb) })
	ctx := context.Background()
	_, err = audit_repo.NewAudit().ListSessions(ctx, end-day)
	require.NoError(t, err)
	_, _, err = audit_repo.NewAudit().List(ctx, audit_repo.ListOptions{Source: "opsctl", StartTime: end - day, Limit: 20})
	require.NoError(t, err)
	require.NotEmpty(t, rec.stmts)

	for _, stmt := range rec.stmts {
		var plan []struct{ Detail string }
		require.NoError(t, gdb.Raw("EXPLAIN QUERY PLAN "+stmt).Scan(&plan).Error)
		var b strings.Builder
		for _, p := range plan {
			b.WriteString(p.Detail + "\n")
		}
		require.Contains(t, b.String(), "SEARCH audit_logs", "sql=%s plan=%s", stmt, b.String())
		require.NotContains(t, b.String(), "SCAN audit_logs", "sql=%s", stmt)
	}
}
