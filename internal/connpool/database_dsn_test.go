package connpool

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestOpenWithTunnelMSSQLRouted(t *testing.T) {
	Convey("openWithTunnel 把 MSSQL 路由到专用分支", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverMSSQL, Host: "h", Port: 1433,
			Username: "u", Database: "d",
		}
		_, err := openWithTunnel(cfg, "pw", nil) // tunnel=nil 会在分支内部出错,但不应是"不支持"错误
		if err != nil {
			So(err.Error(), ShouldNotContainSubstring, "不支持的数据库驱动")
		}
	})
}

func TestBuildDSNMSSQL(t *testing.T) {
	Convey("MSSQL DSN", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver:   asset_entity.DriverMSSQL,
			Host:     "sql.example.com",
			Port:     1433,
			Username: "sa",
			Database: "appdb",
		}
		driverName, dsn := buildDSN(cfg, "p@ss!w0rd")
		So(driverName, ShouldEqual, "sqlserver")

		u, err := url.Parse(dsn)
		So(err, ShouldBeNil)
		So(u.Scheme, ShouldEqual, "sqlserver")
		So(u.Host, ShouldEqual, "sql.example.com:1433")
		So(u.User.Username(), ShouldEqual, "sa")
		pw, _ := u.User.Password()
		So(pw, ShouldEqual, "p@ss!w0rd")
		So(u.Query().Get("database"), ShouldEqual, "appdb")
		So(u.Query().Get("encrypt"), ShouldEqual, "disable")
	})

	Convey("MSSQL DSN with TLS", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverMSSQL, Host: "h", Port: 1433,
			Username: "u", Database: "d", TLS: true,
		}
		_, dsn := buildDSN(cfg, "pw")
		So(strings.Contains(dsn, "encrypt=true"), ShouldBeTrue)
		So(strings.Contains(dsn, "trustservercertificate=true"), ShouldBeTrue)
	})
}

func TestSetReadOnlyMSSQLNoop(t *testing.T) {
	Convey("MSSQL setReadOnly 是 no-op 不报错", t, func() {
		db, mock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		err = setReadOnly(context.Background(), db, asset_entity.DriverMSSQL)
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil) // 没有 ExpectExec，任何调用都会失败
	})
}

func TestBuildDSNSQLite(t *testing.T) {
	Convey("SQLite DSN", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite,
			Path:   "/tmp/test.db",
		}
		driverName, dsn := buildDSN(cfg, "")
		So(driverName, ShouldEqual, "sqlite")
		So(dsn, ShouldEqual, "file:/tmp/test.db")
	})

	Convey("SQLite DSN with params", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
			Params: "_pragma=busy_timeout(5000)",
		}
		_, dsn := buildDSN(cfg, "")
		So(dsn, ShouldEqual, "file:/tmp/x.db?_pragma=busy_timeout(5000)")
	})
}
