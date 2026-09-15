package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 迁移跑在用户已有的库上，且 gormigrate 的记账表可能因为回滚/手工修库而丢失，
// 所以同一条迁移必须能重复执行而不报错。
func TestMigration202609140001_Repeatable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migration202609140001?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE ai_providers (id INTEGER PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE conversations (id INTEGER PRIMARY KEY, title TEXT)`).Error)

	m := migration202609140001()
	require.NoError(t, m.Migrate(db), "首次执行")
	require.NoError(t, m.Migrate(db), "重复执行不能报错")

	assert.True(t, db.Migrator().HasColumn("ai_providers", "extra_headers"))
	assert.True(t, db.Migrator().HasColumn("conversations", "external_session_id"))
}
