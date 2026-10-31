package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExtensionStateProvenanceMigration_RepeatableAndKeepsRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ext_state_prov?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE extension_state (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(255), enabled NUMERIC, createtime INTEGER, updatetime INTEGER)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO extension_state (name, enabled) VALUES ('old', 1)`).Error)

	m := migration202610310005()
	require.NoError(t, m.Migrate(db))
	// A rerun (a dev database that already has the columns): no error.
	require.NoError(t, m.Migrate(db))

	var row struct {
		Source        string
		PackageSha256 string `gorm:"column:package_sha256"`
	}
	require.NoError(t, db.Table("extension_state").Where("name = ?", "old").Take(&row).Error)
	assert.Empty(t, row.Source, "existing rows have an unknown source, never store")
	assert.Empty(t, row.PackageSha256)
}

func TestExtensionStateProvenanceMigration_SkipsColumnsAlreadyThere(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ext_state_prov2?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE extension_state (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(255), source VARCHAR(16) DEFAULT '', package_sha256 VARCHAR(64) DEFAULT '')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO extension_state (name, source, package_sha256) VALUES ('x', 'store', 'abc')`).Error)

	require.NoError(t, migration202610310005().Migrate(db))

	var src string
	require.NoError(t, db.Table("extension_state").Select("source").Where("name = ?", "x").Scan(&src).Error)
	assert.Equal(t, "store", src)
}
