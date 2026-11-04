package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExtensionSettingsMigration_RepeatableAndUniquePerExtension(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ext_settings?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	m := migration202610310004()
	require.NoError(t, m.Migrate(db))
	require.NoError(t, db.Exec(`INSERT INTO extension_settings (extension_name, value, createtime, updatetime) VALUES ('jev', '{"a":1}', 1, 1)`).Error)
	// Run again (a dev database that already ran it): no error, rows kept.
	require.NoError(t, m.Migrate(db))

	var n int64
	require.NoError(t, db.Table("extension_settings").Count(&n).Error)
	assert.EqualValues(t, 1, n)
	assert.Error(t, db.Exec(`INSERT INTO extension_settings (extension_name, value, createtime, updatetime) VALUES ('jev', '{}', 2, 2)`).Error,
		"one settings row per extension")
}

func TestExtensionSettingsMigration_RunsThroughGormigrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ext_settings_fresh?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{migration202610310004()}).Migrate())
	assert.True(t, db.Migrator().HasTable("extension_settings"))
	assert.True(t, db.Migrator().HasIndex("extension_settings", "uq_extension_settings_extension_name"))
}
