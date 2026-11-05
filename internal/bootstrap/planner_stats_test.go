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

func openMigratedDB(t *testing.T) (path string, gdb *gorm.DB) {
	t.Helper()
	dataDir := t.TempDir()
	path = filepath.Join(dataDir, "opskat.db")
	gdb = openSQLite(t, path)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))
	return path, gdb
}

func openSQLite(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

// seedAuditRows 把 audit_logs 补到 upTo 行。
func seedAuditRows(t *testing.T, gdb *gorm.DB, upTo int) {
	t.Helper()
	require.NoError(t, gdb.Exec(fmt.Sprintf(`INSERT INTO audit_logs (source, tool_name, session_id, createtime)
WITH RECURSIVE n(i) AS (SELECT (SELECT COUNT(*) FROM audit_logs) + 1 UNION ALL SELECT i+1 FROM n WHERE i < %d)
SELECT 'ai', 'exec', 'sess-' || (i %% 7), i FROM n`, upTo)).Error)
}

// 统计已跟上数据时启动刷新不取写锁：桌面端与 opsctl 是两个进程，一方正在写库时，
// 另一方启动不能因为刷新统计卡在 busy_timeout 上。
func TestRefreshPlannerStatsTakesNoWriteLockWhenStatsAreFresh(t *testing.T) {
	path, gdb := openMigratedDB(t)
	seedAuditRows(t, gdb, 50)
	require.NoError(t, refreshPlannerStats(gdb))

	writer := openSQLite(t, path)
	require.NoError(t, writer.Exec("BEGIN IMMEDIATE").Error)
	t.Cleanup(func() { _ = writer.Exec("ROLLBACK").Error })

	starting := openSQLite(t, path+"?_pragma=busy_timeout(0)")
	require.NoError(t, refreshPlannerStats(starting))
}

// 审计日志在上次统计之后涨了一个数量级：刷新后规划器看到的行数跟上当前规模。
func TestRefreshPlannerStatsFollowsAuditGrowth(t *testing.T) {
	_, gdb := openMigratedDB(t)
	seedAuditRows(t, gdb, 20)
	require.NoError(t, refreshPlannerStats(gdb))
	seedAuditRows(t, gdb, 2000)
	require.NoError(t, refreshPlannerStats(gdb))

	var stat string
	require.NoError(t, gdb.Raw(`SELECT stat FROM sqlite_stat1 WHERE tbl = 'audit_logs' LIMIT 1`).Scan(&stat).Error)
	require.Equal(t, "2000", strings.Fields(stat)[0], "planner row count must follow the table, got stat %q", stat)
}
