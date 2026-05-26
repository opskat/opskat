package connpool

import (
	"net/url"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	. "github.com/smartystreets/goconvey/convey"
)

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
