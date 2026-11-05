package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuditWindowIndexesMigration_CreatesIndexesAndIsRepeatable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:audit_window_idx?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE audit_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, source VARCHAR(20), session_id VARCHAR(64), createtime INTEGER NOT NULL)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO audit_logs (source, session_id, createtime) VALUES ('ai', 's1', 1)`).Error)

	m := migration202611050001()
	require.NoError(t, m.Migrate(db))
	require.NoError(t, m.Migrate(db))

	for _, name := range []string{"idx_audit_session_createtime", "idx_audit_source_createtime"} {
		var n int
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, name).Scan(&n).Error)
		assert.Equal(t, 1, n, name)
	}
	var stat int
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl = 'audit_logs'`).Scan(&stat).Error)
	assert.Positive(t, stat, "ANALYZE must leave statistics for the planner")
}
