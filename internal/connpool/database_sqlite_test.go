package connpool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	_ "github.com/opskat/opskat/internal/dbdriver/sqlite"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// SQLite 自己的连接串、只读与远端打开的断言在 internal/dbdriver/sqlite；这里测的是经共享
// 入口走到它的这一段。
func TestOpenDatabaseSQLite(t *testing.T) {
	Convey("SQLite 本地文件直连", t, func() {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
		cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: dbPath}

		session, err := OpenDatabase(context.Background(), asset, cfg, "", nil)
		So(err, ShouldBeNil)
		So(session, ShouldNotBeNil)
		defer func() { _ = session.Close() }()

		_, execErr := session.Execute(context.Background(), "CREATE TABLE t (id INTEGER)")
		So(execErr, ShouldBeNil)
		_, err = os.Stat(dbPath)
		So(err, ShouldBeNil)
	})

	Convey("SQLite ReadOnly 经共享入口同样生效", t, func() {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
		rw, err := OpenDatabase(context.Background(), asset,
			&asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: dbPath}, "", nil)
		So(err, ShouldBeNil)
		_, err = rw.Execute(context.Background(), "CREATE TABLE t (id INTEGER)")
		So(err, ShouldBeNil)
		So(rw.Close(), ShouldBeNil)

		ro, err := OpenDatabase(context.Background(), asset,
			&asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: dbPath, ReadOnly: true}, "", nil)
		So(err, ShouldBeNil)
		defer func() { _ = ro.Close() }()

		_, execErr := ro.Execute(context.Background(), "INSERT INTO t VALUES (1)")
		So(execErr, ShouldNotBeNil)
		So(execErr.Error(), ShouldContainSubstring, "read")
	})

	Convey("远端 SQLite VFS 没有 SSH 连接池时失败", t, func() {
		asset := &asset_entity.Asset{
			ID: 1, Type: asset_entity.AssetTypeDatabase, SSHTunnelID: 9,
		}
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, SQLiteSource: asset_entity.SQLiteSourceRemoteSSHVFS,
			Path: "/tmp/remote.db",
		}

		session, err := OpenDatabase(context.Background(), asset, cfg, "", nil)
		So(session, ShouldBeNil)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "SSH")
	})

	Convey("远端 SQLite VFS 没有 SSH 资产时失败", t, func() {
		asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, SQLiteSource: asset_entity.SQLiteSourceRemoteSSHVFS,
			Path: "/tmp/remote.db",
		}

		session, err := OpenDatabase(context.Background(), asset, cfg, "", nil)
		So(session, ShouldBeNil)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "SSH")
	})
}
