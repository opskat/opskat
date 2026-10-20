package postgresql

import (
	"context"
	"errors"
	"net"
	"runtime"
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

func TestPostgreSQLPassesConformance(t *testing.T) {
	d, err := dbregistry.Lookup(asset_entity.DriverPostgreSQL)
	require.NoError(t, err)

	dbdrivertest.RunConformance(t, d, func(t *testing.T) dbdriver.Session {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		one := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"?column?"}).AddRow(1) }
		// Execute、分页的计数、分页的取数，三条语句里都含 ConformanceStatement。
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectClose()
		return newSession(db)
	})
}

func TestDialConnectorPassesAddr(t *testing.T) {
	Convey("dialConnector 把目标网络与地址透传给 dial", t, func() {
		cfg := &dbdriver.Config{
			Driver: asset_entity.DriverPostgreSQL, Host: "pg.internal", Port: 5433,
			Username: "u", Database: "d",
		}
		var gotNetwork, gotAddr string
		connector := newDialConnector(dsn(cfg, "pw"), func(ctx context.Context, network, addr string) (net.Conn, error) {
			gotNetwork, gotAddr = network, addr
			return nil, errors.New("dial-sentinel")
		})
		_, err := connector.Connect(context.Background())
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "dial-sentinel")
		So(gotNetwork, ShouldEqual, "tcp")
		So(gotAddr, ShouldEqual, "pg.internal:5433")
	})

	if runtime.GOOS == "windows" {
		return // Unix socket 主机只在 Unix 客户端上有意义，pgx 在 Windows 上不把 /tmp 当 socket 目录
	}
	Convey("dialConnector 对 unix socket 主机透传 unix 网络(直连本机 PG)", t, func() {
		connStr := dsn(&dbdriver.Config{
			Driver: asset_entity.DriverPostgreSQL, Host: "localhost", Port: 5432,
			Username: "u", Database: "d", Params: "host=/tmp",
		}, "pw")
		var gotNetwork, gotAddr string
		connector := newDialConnector(connStr, func(ctx context.Context, network, addr string) (net.Conn, error) {
			gotNetwork, gotAddr = network, addr
			return nil, errors.New("dial-sentinel")
		})
		_, err := connector.Connect(context.Background())
		So(err, ShouldNotBeNil)
		So(gotNetwork, ShouldEqual, "unix")
		So(gotAddr, ShouldEqual, "/tmp/.s.PGSQL.5432")
	})
}

func TestDSN(t *testing.T) {
	cfg := &dbdriver.Config{
		Driver: asset_entity.DriverPostgreSQL, Host: "pg.internal", Port: 5433,
		Username: "app user", Database: "my db",
	}
	// 用户名、口令、库名里的特殊字符要转义；没配 ssl_mode 时不加密。
	assert.Equal(t, "postgres://app+user:p%40ss%2Fw0rd@pg.internal:5433/my%20db?sslmode=disable", dsn(cfg, "p@ss/w0rd"))

	cfg.SSLMode = "require"
	cfg.Params = "application_name=opskat"
	assert.Equal(t, "postgres://app+user:pw@pg.internal:5433/my%20db?sslmode=require&application_name=opskat", dsn(cfg, "pw"))
}

// 给了拨号函数，到数据库的连接就必须经它建立（隧道 / 代理 / 代理链 / .local）。
func TestOpenDialsThroughTheTransport(t *testing.T) {
	var dialed atomic.Value
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := driver{}.Open(ctx, dbdriver.OpenRequest{
		Config:   &dbdriver.Config{Driver: asset_entity.DriverPostgreSQL, Host: "pg.internal", Port: 5433, Username: "u", Database: "d"},
		Password: "pw",
		Transport: dbdriver.Transport{Dial: func(_ context.Context, network, addr string) (net.Conn, error) {
			dialed.Store(network + " " + addr)
			return nil, errors.New("dial-sentinel")
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "数据库连接失败")
	assert.Contains(t, err.Error(), "dial-sentinel")
	assert.Equal(t, "tcp pg.internal:5433", dialed.Load())
}

func TestSetReadOnly(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("SET default_transaction_read_only = on").WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, setReadOnly(context.Background(), db))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutePagedDialect(t *testing.T) {
	Convey("PostgreSQL 分页用 LIMIT/OFFSET", t, func() {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		mock.ExpectQuery("SELECT * FROM (SELECT * FROM users) AS _t LIMIT 10 OFFSET 20").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

		_, err = newSession(db).ExecutePaged(context.Background(), "SELECT * FROM users", dbdriver.Page{Index: 2, Size: 10})
		So(err, ShouldBeNil)
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}
