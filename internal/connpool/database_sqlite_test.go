package connpool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestDialDatabaseSQLite(t *testing.T) {
	Convey("SQLite 本地文件直连", t, func() {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")

		asset := &asset_entity.Asset{
			ID: 1, Type: asset_entity.AssetTypeDatabase,
		}
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, Path: dbPath,
		}
		db, closer, err := DialDatabase(context.Background(), asset, cfg, "", nil)
		So(err, ShouldBeNil)
		So(db, ShouldNotBeNil)
		So(closer, ShouldBeNil) // SQLite 无隧道,closer 应为 nil
		defer func() { _ = db.Close() }()

		_, execErr := db.Exec("CREATE TABLE t (id INTEGER)")
		So(execErr, ShouldBeNil)
		_, err = os.Stat(dbPath)
		So(err, ShouldBeNil)
	})

	Convey("SQLite ReadOnly 用 PRAGMA query_only", t, func() {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")

		asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
		// 先建表
		initCfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: dbPath}
		db, _, err := DialDatabase(context.Background(), asset, initCfg, "", nil)
		So(err, ShouldBeNil)
		_, err = db.Exec("CREATE TABLE t (id INTEGER)")
		So(err, ShouldBeNil)
		_ = db.Close()

		// 再以 ReadOnly 打开
		roCfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, Path: dbPath, ReadOnly: true,
		}
		roDB, _, err := DialDatabase(context.Background(), asset, roCfg, "", nil)
		So(err, ShouldBeNil)
		defer func() { _ = roDB.Close() }()

		_, execErr := roDB.Exec("INSERT INTO t VALUES (1)")
		So(execErr, ShouldNotBeNil)
		So(execErr.Error(), ShouldContainSubstring, "read")
	})
}
