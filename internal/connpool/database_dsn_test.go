package connpool

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

func TestOpenWithDialerMSSQLRouted(t *testing.T) {
	Convey("openWithDialer 把 MSSQL 路由到专用分支", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverMSSQL, Host: "h", Port: 1433,
			Username: "u", Database: "d",
		}
		_, err := openWithDialer(cfg, "pw", nil) // dial=nil 仅在连接时使用,open 阶段不应是"不支持"错误
		if err != nil {
			So(err.Error(), ShouldNotContainSubstring, "不支持的数据库驱动")
		}
	})
}

func TestPgDialConnectorPassesAddr(t *testing.T) {
	Convey("pgDialConnector 把目标地址透传给 dial", t, func() {
		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverPostgreSQL, Host: "pg.internal", Port: 5433,
			Username: "u", Database: "d",
		}
		_, dsn := buildDSN(cfg, "pw")
		var gotAddr string
		connector := newPgDialConnector(dsn, func(ctx context.Context, addr string) (net.Conn, error) {
			gotAddr = addr
			return nil, errors.New("dial-sentinel")
		})
		_, err := connector.Connect(context.Background())
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "dial-sentinel")
		So(gotAddr, ShouldEqual, "pg.internal:5433")
	})
}

func TestRegisterMySQLDialerUnique(t *testing.T) {
	Convey("registerMySQLDialer 每次返回唯一注册名", t, func() {
		dial := func(ctx context.Context, addr string) (net.Conn, error) {
			return nil, errors.New("unused")
		}
		n1 := registerMySQLDialer(dial)
		n2 := registerMySQLDialer(dial)
		So(n1, ShouldNotEqual, n2)
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
