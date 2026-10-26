package mssql

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestMSSQLPassesConformance(t *testing.T) {
	d, err := dbregistry.Lookup(asset_entity.DriverMSSQL)
	require.NoError(t, err)

	dbdrivertest.RunConformance(t, d, func(t *testing.T) dbdriver.Session {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		one := func() *sqlmock.Rows { return sqlmock.NewRows([]string{""}).AddRow(1) }
		// Execute、分页的计数、分页的取数，三条语句里都含 ConformanceStatement。
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectClose()
		return newSession(db)
	})
}

// 给了拨号函数，到数据库的连接就必须经它建立（隧道 / 代理 / 代理链 / .local）。
// 主机用 IP 字面量：go-mssqldb 在调用 Dialer 之前会先在本机解析主机名。
func TestOpenDialsThroughTheTransport(t *testing.T) {
	var dialed atomic.Value
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := driver{}.Open(ctx, dbdriver.OpenRequest{
		Config:   &dbdriver.Config{Driver: asset_entity.DriverMSSQL, Host: "10.9.8.7", Port: 1434, Username: "u", Database: "d"},
		Password: "pw",
		Transport: dbdriver.Transport{Dial: func(_ context.Context, network, addr string) (net.Conn, error) {
			dialed.Store(network + " " + addr)
			return nil, errors.New("dial-sentinel")
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "数据库连接失败")
	assert.Equal(t, "tcp 10.9.8.7:1434", dialed.Load())
}

func TestDSN(t *testing.T) {
	Convey("MSSQL DSN", t, func() {
		cfg := &dbdriver.Config{
			Driver:   asset_entity.DriverMSSQL,
			Host:     "sql.example.com",
			Port:     1433,
			Username: "sa",
			Database: "appdb",
		}
		u, err := url.Parse(dsn(cfg, "p@ss!w0rd"))
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
		cfg := &dbdriver.Config{
			Driver: asset_entity.DriverMSSQL, Host: "h", Port: 1433,
			Username: "u", Database: "d", TLS: true,
		}
		connStr := dsn(cfg, "pw")
		So(strings.Contains(connStr, "encrypt=true"), ShouldBeTrue)
		So(strings.Contains(connStr, "trustservercertificate=true"), ShouldBeTrue)
	})
}

func TestSetReadOnlyNoop(t *testing.T) {
	Convey("MSSQL setReadOnly 是 no-op 不报错", t, func() {
		db, mock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		err = setReadOnly(context.Background(), db)
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil) // 没有 ExpectExec，任何调用都会失败
	})
}

func TestExecutePagedDialect(t *testing.T) {
	Convey("MSSQL 分页用 OFFSET/FETCH 而非 LIMIT", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		mock.ExpectQuery(
			"SELECT * FROM (SELECT * FROM users) AS _t ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))

		_, err = newSession(db).ExecutePaged(context.Background(), "SELECT * FROM users", dbdriver.Page{Index: 0, Size: 10})
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})

	Convey("MSSQL 分页保留顶层 ORDER BY 且 count 去掉 ORDER BY", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		mock.ExpectQuery("SELECT * FROM users ORDER BY id DESC OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))

		_, err = newSession(db).ExecutePaged(context.Background(), "SELECT * FROM users ORDER BY id DESC", dbdriver.Page{Index: 2, Size: 10})
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}
