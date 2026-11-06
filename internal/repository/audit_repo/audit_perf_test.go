package audit_repo

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

	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/migrations"
)

const (
	perfRows = 200000
	perfDay  = int64(86400)
	perfEnd  = int64(1_800_000_000)
)

// sqlRecorder is a gorm logger that records every statement the repo issues.
type sqlRecorder struct {
	gormlogger.Interface
	stmts []string
}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.stmts = append(r.stmts, sql)
}

// seedLegacyAuditDB builds a temp sqlite in the state of an existing user's
// database before the audit index migration (populated table, no new indexes,
// no planner statistics) with perfRows audit rows spread over 60 days, 4000
// sessions and 3 sources. upgrade re-runs the migrations like an app update.
func seedLegacyAuditDB(t *testing.T) (gdb *gorm.DB, upgrade func()) {
	t.Helper()
	dataDir := t.TempDir()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))
	// Bulk-load fast: one connection with relaxed durability, and the
	// pre-existing secondary indexes built once after the insert instead of
	// maintained per row.
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, gdb.Exec("PRAGMA synchronous = OFF").Error)
	require.NoError(t, gdb.Exec("PRAGMA journal_mode = MEMORY").Error)
	legacyIndexes := map[string]string{
		"idx_audit_source":          "source",
		"idx_audit_asset_id":        "asset_id",
		"idx_audit_conversation_id": "conversation_id",
		"idx_audit_session_id":      "session_id",
		"idx_audit_createtime":      "createtime",
	}
	for name := range legacyIndexes {
		require.NoError(t, gdb.Exec("DROP INDEX IF EXISTS "+name).Error)
	}
	require.NoError(t, gdb.Exec(fmt.Sprintf(`INSERT INTO audit_logs (source, tool_name, session_id, createtime)
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < %d)
SELECT CASE i %% 20 WHEN 0 THEN 'opsctl' WHEN 1 THEN 'ui' ELSE 'ai' END,
       'exec',
       CASE WHEN i %% 10 = 0 THEN '' ELSE 'sess-' || (i %% 4000) END,
       %d - (%d - i) * (60 * %d) / %d
FROM n`, perfRows, perfEnd, perfRows, perfDay, perfRows)).Error)
	for name, col := range legacyIndexes {
		require.NoError(t, gdb.Exec("CREATE INDEX "+name+" ON audit_logs ("+col+")").Error)
	}
	for _, stmt := range []string{
		"DROP INDEX IF EXISTS idx_audit_session_createtime",
		"DROP INDEX IF EXISTS idx_audit_source_createtime",
		"DELETE FROM migrations WHERE id = '202611050001'",
	} {
		require.NoError(t, gdb.Exec(stmt).Error)
	}
	if gdb.Migrator().HasTable("sqlite_stat1") {
		require.NoError(t, gdb.Exec("DELETE FROM sqlite_stat1").Error)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb, func() { require.NoError(t, migrations.RunMigrations(gdb, dataDir)) }
}

// record runs fn against the repo and returns the SELECT statements it issued.
func record(t *testing.T, gdb *gorm.DB, fn func(ctx context.Context)) []string {
	t.Helper()
	rec := &sqlRecorder{Interface: gormlogger.Discard}
	db.SetDefault(gdb.Session(&gorm.Session{Logger: rec}))
	fn(context.Background())
	var out []string
	for _, s := range rec.stmts {
		if strings.HasPrefix(s, "SELECT") {
			out = append(out, s)
		}
	}
	require.NotEmpty(t, out)
	return out
}

func plan(t *testing.T, gdb *gorm.DB, sql string) string {
	t.Helper()
	var rows []struct {
		Detail string
	}
	require.NoError(t, gdb.Raw("EXPLAIN QUERY PLAN "+sql).Scan(&rows).Error)
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.Detail + "\n")
	}
	return b.String()
}

// Every statement must be bounded by an index range: SQLite reports an
// unbounded walk as "SCAN audit_logs" (with or without USING INDEX).
func requireWindowSearch(t *testing.T, gdb *gorm.DB, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		p := plan(t, gdb, s)
		require.Contains(t, p, "SEARCH audit_logs", "sql=%s plan=%s", s, p)
		require.NotRegexp(t, `SCAN audit_logs`, p, "sql=%s", s)
	}
}

type auditQueryResult struct {
	sessions []SessionInfo
	logs     []*audit_entity.AuditLog
	total    int64
	sessDur  time.Duration
	listDur  time.Duration
}

func runAuditQueries(t *testing.T, gdb *gorm.DB) (res auditQueryResult, sessionSQL, listSQL []string) {
	t.Helper()
	repo := NewAudit()
	start := perfEnd - perfDay
	sessionSQL = record(t, gdb, func(ctx context.Context) {
		t0 := time.Now()
		var err error
		res.sessions, err = repo.ListSessions(ctx, start)
		res.sessDur = time.Since(t0)
		require.NoError(t, err)
	})
	listSQL = record(t, gdb, func(ctx context.Context) {
		t0 := time.Now()
		var err error
		res.logs, res.total, err = repo.List(ctx, ListOptions{Source: "opsctl", StartTime: start, Limit: 20})
		res.listDur = time.Since(t0)
		require.NoError(t, err)
	})
	return res, sessionSQL, listSQL
}

// TestAuditPerf_UpgradeUsesIndexesAndKeepsResults upgrades a populated legacy DB
// and checks the window queries stop scanning while returning identical rows.
func TestAuditPerf_UpgradeUsesIndexesAndKeepsResults(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 200k audit rows")
	}
	gdb, upgrade := seedLegacyAuditDB(t)

	before, _, _ := runAuditQueries(t, gdb)
	require.NotEmpty(t, before.sessions)
	require.NotEmpty(t, before.logs)

	upgrade()

	after, sessionSQL, listSQL := runAuditQueries(t, gdb)
	requireWindowSearch(t, gdb, sessionSQL)
	requireWindowSearch(t, gdb, listSQL)

	for _, s := range append(sessionSQL, listSQL...) {
		t.Logf("%s\n%s", s, plan(t, gdb, s))
	}
	require.Equal(t, before.sessions, after.sessions)
	require.Equal(t, before.logs, after.logs)
	require.Equal(t, before.total, after.total)

	t.Logf("ListSessions(1d) before=%s after=%s; List(source) before=%s after=%s",
		before.sessDur, after.sessDur, before.listDur, after.listDur)
}
