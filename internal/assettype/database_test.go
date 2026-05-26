package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/smartystreets/goconvey/convey"
)

func TestDatabaseHandlerApplyCreateArgsSQLite(t *testing.T) {
	convey.Convey("SQLite ApplyCreateArgs 写入 Path 字段", t, func() {
		h := &databaseHandler{}
		a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
		err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
			"driver": "sqlite",
			"path":   "/tmp/x.db",
		})
		convey.So(err, convey.ShouldBeNil)
		cfg, err := a.GetDatabaseConfig()
		convey.So(err, convey.ShouldBeNil)
		convey.So(cfg.Driver, convey.ShouldEqual, asset_entity.DriverSQLite)
		convey.So(cfg.Path, convey.ShouldEqual, "/tmp/x.db")
		convey.So(cfg.Host, convey.ShouldEqual, "")
	})
}

func TestDatabaseHandlerSafeViewSQLite(t *testing.T) {
	convey.Convey("SQLite SafeView 返回 path 不返回 host", t, func() {
		h := &databaseHandler{}
		a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
		_ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
		})
		view := h.SafeView(a)
		convey.So(view["driver"], convey.ShouldEqual, "sqlite")
		convey.So(view["path"], convey.ShouldEqual, "/tmp/x.db")
		_, hasHost := view["host"]
		convey.So(hasHost, convey.ShouldBeFalse)
	})
}

func TestDatabaseHandler(t *testing.T) {
	convey.Convey("Database Handler", t, func() {
		h := &databaseHandler{}
		convey.Convey("Type and DefaultPort", func() {
			convey.So(h.Type(), convey.ShouldEqual, "database")
			convey.So(h.DefaultPort(), convey.ShouldEqual, 3306)
		})
		convey.Convey("SafeView", func() {
			a := &asset_entity.Asset{Type: "database", Status: 1}
			_ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
				Driver: asset_entity.DriverMySQL, Host: "10.0.0.1", Port: 3306,
				Username: "admin", Password: "secret", Database: "mydb", ReadOnly: true,
			})
			view := h.SafeView(a)
			convey.So(view["host"], convey.ShouldEqual, "10.0.0.1")
			convey.So(view["port"], convey.ShouldEqual, 3306)
			convey.So(view["username"], convey.ShouldEqual, "admin")
			convey.So(view["driver"], convey.ShouldEqual, "mysql")
			convey.So(view["database"], convey.ShouldEqual, "mydb")
			convey.So(view["read_only"], convey.ShouldEqual, true)
			_, hasPassword := view["password"]
			convey.So(hasPassword, convey.ShouldBeFalse)
		})
		convey.Convey("ApplyCreateArgs", func() {
			a := &asset_entity.Asset{Type: "database"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"driver": "mysql", "host": "10.0.0.1", "port": float64(3306),
				"username": "admin", "database": "mydb", "read_only": "true",
				"ssh_asset_id": float64(42),
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetDatabaseConfig()
			convey.So(cfg.Driver, convey.ShouldEqual, asset_entity.DriverMySQL)
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.1")
			convey.So(cfg.Port, convey.ShouldEqual, 3306)
			convey.So(cfg.Database, convey.ShouldEqual, "mydb")
			convey.So(cfg.ReadOnly, convey.ShouldBeTrue)
			convey.So(cfg.SSHAssetID, convey.ShouldEqual, 42)
		})
		convey.Convey("ApplyCreateArgs requires driver", func() {
			a := &asset_entity.Asset{Type: "database"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"host": "10.0.0.1", "port": float64(3306),
			})
			convey.So(err, convey.ShouldNotBeNil)
		})
		convey.Convey("ApplyUpdateArgs", func() {
			a := &asset_entity.Asset{Type: "database"}
			_ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
				Driver: asset_entity.DriverMySQL, Host: "10.0.0.1", Port: 3306,
				Username: "admin", Database: "mydb",
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"host": "10.0.0.2", "database": "newdb"})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetDatabaseConfig()
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.2")
			convey.So(cfg.Port, convey.ShouldEqual, 3306)
			convey.So(cfg.Username, convey.ShouldEqual, "admin")
			convey.So(cfg.Database, convey.ShouldEqual, "newdb")
		})
		convey.Convey("Registered", func() {
			h, ok := Get("database")
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(h.Type(), convey.ShouldEqual, "database")
		})
	})
}
