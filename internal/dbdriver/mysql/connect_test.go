package mysql

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestMySQLPassesConformance(t *testing.T) {
	d, err := dbdriver.Lookup(asset_entity.DriverMySQL)
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
	cfg := &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverMySQL, Host: "db.internal", Port: 3307,
		Username: "root", Database: "app", TLS: true, Params: "sql_mode=ANSI",
	}

	t.Run("direct connections use the driver's own TCP dialing", func(t *testing.T) {
		parsed, err := gomysql.ParseDSN(dsn(cfg, "p@ss:w0rd", nil))
		require.NoError(t, err)
		assert.Equal(t, "tcp", parsed.Net)
		assert.Equal(t, "db.internal:3307", parsed.Addr)
		assert.Equal(t, "root", parsed.User)
		assert.Equal(t, "p@ss:w0rd", parsed.Passwd)
		assert.Equal(t, "app", parsed.DBName)
		assert.Equal(t, "skip-verify", parsed.TLSConfig)
		assert.Equal(t, map[string]string{"sql_mode": "ANSI"}, parsed.Params)
	})

	t.Run("a transport dialer replaces the network", func(t *testing.T) {
		dial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") }
		parsed, err := gomysql.ParseDSN(dsn(cfg, "pw", dial))
		require.NoError(t, err)
		assert.NotEqual(t, "tcp", parsed.Net)
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
		Config:   &asset_entity.DatabaseConfig{Driver: asset_entity.DriverMySQL, Host: "db.internal", Port: 3307, Username: "u"},
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
