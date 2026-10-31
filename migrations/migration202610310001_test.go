package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newOwnerMigrationDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE assets (id INTEGER PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE audit_logs (id INTEGER PRIMARY KEY, tool_name TEXT)`).Error)
	return db
}

func assertOwnerSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	assert.True(t, db.Migrator().HasTable("command_reviews"))
	assert.True(t, db.Migrator().HasIndex("command_reviews", "uq_command_reviews_cache_key"))
	assert.True(t, db.Migrator().HasIndex("command_reviews", "idx_command_reviews_expiretime"))
	assert.True(t, db.Migrator().HasColumn("assets", "permission_mode"))
	assert.True(t, db.Migrator().HasColumn("groups", "permission_mode"))
	assert.True(t, db.Migrator().HasColumn("audit_logs", "review"))
}

func ownerMigrations() []*gormigrate.Migration {
	return []*gormigrate.Migration{migration202610310001(), migration202610310002(), migration202610310003()}
}

func TestOwnerMigrations_Repeatable(t *testing.T) {
	db := newOwnerMigrationDB(t, "owner_repeatable")
	for i := 0; i < 2; i++ {
		for _, m := range ownerMigrations() {
			require.NoError(t, m.Migrate(db), m.ID)
		}
	}
	assertOwnerSchema(t, db)
}

// 已记录旧迁移 202609290001 为已执行的开发库（表列部分已存在）再跑新迁移，不报错且结构完整。
func TestOwnerMigrations_AfterLegacyID(t *testing.T) {
	db := newOwnerMigrationDB(t, "owner_legacy")
	require.NoError(t, db.Exec(`CREATE TABLE command_reviews (id INTEGER PRIMARY KEY AUTOINCREMENT, cache_key VARCHAR(64) NOT NULL, result TEXT NOT NULL, createtime INTEGER, expiretime INTEGER)`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE assets ADD COLUMN permission_mode VARCHAR(20) DEFAULT ''`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE migrations (id VARCHAR(255) PRIMARY KEY)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO migrations (id) VALUES ('202609290001')`).Error)

	require.NoError(t, gormigrate.New(db, gormigrate.DefaultOptions, ownerMigrations()).Migrate())
	assertOwnerSchema(t, db)
}

func TestOwnerMigrations_FreshDB(t *testing.T) {
	db := newOwnerMigrationDB(t, "owner_fresh")
	require.NoError(t, gormigrate.New(db, gormigrate.DefaultOptions, ownerMigrations()).Migrate())
	assertOwnerSchema(t, db)
}
