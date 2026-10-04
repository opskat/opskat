package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigration202609290001_Repeatable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migration202609290001?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE assets (id INTEGER PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE audit_logs (id INTEGER PRIMARY KEY, tool_name TEXT)`).Error)

	m := migration202609290001()
	require.NoError(t, m.Migrate(db), "首次执行")
	require.NoError(t, m.Migrate(db), "重复执行不能报错")

	assert.True(t, db.Migrator().HasTable("command_reviews"))
	assert.True(t, db.Migrator().HasIndex("command_reviews", "uq_command_reviews_cache_key"))
	assert.True(t, db.Migrator().HasColumn("assets", "permission_mode"))
	assert.True(t, db.Migrator().HasColumn("groups", "permission_mode"))
	assert.True(t, db.Migrator().HasColumn("audit_logs", "review"))
}
