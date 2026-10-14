package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigration202610140001_Repeatable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migration202610140001?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE extension_state (
		id INTEGER PRIMARY KEY, name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
		createtime INTEGER NOT NULL, updatetime INTEGER NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO extension_state (name, enabled, createtime, updatetime) VALUES ('old', 1, 1, 1)`).Error)

	m := migration202610140001()
	require.NoError(t, m.Migrate(db))
	require.NoError(t, m.Migrate(db), "重复执行不能报错")

	assert.True(t, db.Migrator().HasColumn("extension_state", "source"))
	assert.True(t, db.Migrator().HasColumn("extension_state", "package_sha256"))

	var source, sum string
	require.NoError(t, db.Raw(`SELECT source, package_sha256 FROM extension_state WHERE name = 'old'`).Row().Scan(&source, &sum))
	assert.Empty(t, source)
	assert.Empty(t, sum)
}
