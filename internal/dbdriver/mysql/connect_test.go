package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestMySQLPassesConformance(t *testing.T) {
	d, err := dbregistry.Lookup(asset_entity.DriverMySQL)
	require.NoError(t, err)

	dbdrivertest.RunConformance(t, d, func(t *testing.T) dbdriver.Session {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		one := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"1"}).AddRow(1) }
		// Execute、分页的计数、分页的取数，三条语句里都含 ConformanceStatement。
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectQuery(dbdrivertest.ConformanceStatement).WillReturnRows(one())
		mock.ExpectClose()
		return newSession(db)
	})
}

func TestRegisterDialerUnique(t *testing.T) {
	Convey("registerDialer 每次返回唯一注册名", t, func() {
		dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("unused")
		}
		n1 := registerDialer(dial)
		n2 := registerDialer(dial)
		So(n1, ShouldNotEqual, n2)
	})
}

func TestDSN(t *testing.T) {
	cfg := &dbdriver.Config{
		Driver: asset_entity.DriverMySQL, Host: "db.internal", Port: 3307,
		Username: "root", Database: "app", TLS: true, Params: "sql_mode=ANSI",
	}

	t.Run("direct connections use the driver's own TCP dialing", func(t *testing.T) {
		parsed, err := gomysql.ParseDSN(dsn(cfg, "p@ss:w0rd", "tcp"))
		require.NoError(t, err)
		assert.Equal(t, "tcp", parsed.Net)
		assert.Equal(t, "db.internal:3307", parsed.Addr)
		assert.Equal(t, "root", parsed.User)
		assert.Equal(t, "p@ss:w0rd", parsed.Passwd)
		assert.Equal(t, "app", parsed.DBName)
		assert.Equal(t, "skip-verify", parsed.TLSConfig)
		assert.Equal(t, map[string]string{"sql_mode": "ANSI"}, parsed.Params)
	})

	t.Run("a registered transport dialer replaces the network", func(t *testing.T) {
		parsed, err := gomysql.ParseDSN(dsn(cfg, "pw", "opskat-dialer-7"))
		require.NoError(t, err)
		assert.Equal(t, "opskat-dialer-7", parsed.Net)
		assert.Equal(t, "db.internal:3307", parsed.Addr)
	})
}

// 给了拨号函数，到数据库的连接就必须经它建立（隧道 / 代理 / 代理链 / .local）。
func TestOpenDialsThroughTheTransport(t *testing.T) {
	var dialed atomic.Value
	sentinel := errors.New("dial-sentinel")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := driver{}.Open(ctx, dbdriver.OpenRequest{
		Config:   &dbdriver.Config{Driver: asset_entity.DriverMySQL, Host: "db.internal", Port: 3307, Username: "u"},
		Password: "pw",
		Transport: dbdriver.Transport{Dial: func(_ context.Context, network, addr string) (net.Conn, error) {
			dialed.Store(network + " " + addr)
			return nil, sentinel
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "数据库连接失败")
	assert.Equal(t, "tcp db.internal:3307", dialed.Load())
}

// 拨号函数登记在 go-sql-driver 的全局表里，它连着 SSH 连接池 / 代理。会话用完（或根本没
// 开成）必须摘掉，否则每开一条经隧道的会话就永久留下一项。
func TestTransportDialerIsDeregisteredWhenTheSessionEnds(t *testing.T) {
	var dials atomic.Int64
	dial := func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("dial-sentinel")
	}
	// 用同一个注册名再连一次：拨号函数还被叫到，就是还登记着。
	stillRegistered := func(t *testing.T, network string) bool {
		t.Helper()
		before := dials.Load()
		c := gomysql.NewConfig()
		c.Net, c.Addr = network, "db.internal:3307"
		db, err := sql.Open("mysql", c.FormatDSN())
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.Error(t, db.PingContext(ctx))
		return dials.Load() > before
	}

	t.Run("an open that fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := driver{}.Open(ctx, dbdriver.OpenRequest{
			Config:    &dbdriver.Config{Driver: asset_entity.DriverMySQL, Host: "db.internal", Port: 3307, Username: "u"},
			Transport: dbdriver.Transport{Dial: dial},
		})
		require.Error(t, err)
		require.Positive(t, dials.Load(), "the open must have gone through the transport")

		assert.False(t, stillRegistered(t, fmt.Sprintf("opskat-dialer-%d", dialerSeq.Load())))
	})

	t.Run("a registration that is released", func(t *testing.T) {
		registration := registerDialer(dial)
		require.True(t, stillRegistered(t, string(registration)), "pooled connections redial through it while the session lives")

		require.NoError(t, registration.Close())
		assert.False(t, stillRegistered(t, string(registration)))
	})
}

func TestSetReadOnly(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("SET SESSION TRANSACTION READ ONLY").WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, setReadOnly(context.Background(), db))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutePagedDialect(t *testing.T) {
	Convey("MySQL 分页用 LIMIT/OFFSET", t, func() {
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
