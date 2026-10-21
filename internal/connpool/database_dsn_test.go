package connpool

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	_ "github.com/opskat/opskat/internal/dbdriver/mysql"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/opskat/opskat/internal/sshpool"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 连接串、拨号接入、连接级只读、分页方言的断言都随代码迁到了各驱动包
// （internal/dbdriver/{mysql,postgresql,mssql,sqlite}）；这里只测共享的这一段：
// 按配置的驱动打开会话，以及把隧道 / 代理 / 代理链 / .local 解析成交给驱动的传输层。

// openWithFake 注册假驱动，经 OpenDatabase 打开一条会话并返回驱动实际收到的请求。
func openWithFake(t *testing.T, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig, pool *sshpool.Pool) *dbdrivertest.FakeSession {
	t.Helper()
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	cfg.Driver = fake.Desc.ID

	session, err := OpenDatabase(context.Background(), asset, cfg, "pw", pool)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	fakeSession, ok := session.(*dbdrivertest.FakeSession)
	require.True(t, ok, "the session handed back is the driver's own, not something the shared code built")
	return fakeSession
}

func TestOpenDatabaseRejectsUnregisteredDriver(t *testing.T) {
	_, err := OpenDatabase(context.Background(), &asset_entity.Asset{ID: 3},
		&asset_entity.DatabaseConfig{Driver: "oracle", Host: "127.0.0.1", Port: 1521, Username: "u"}, "pw", nil)
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}

func TestOpenDatabaseHandsTheDriverItsConfigAndPassword(t *testing.T) {
	session := openWithFake(t, &asset_entity.Asset{ID: 3},
		&asset_entity.DatabaseConfig{Host: "10.0.0.5", Port: 7777, Database: "app", ReadOnly: true}, nil)

	assert.Equal(t, "app", session.Request.Config.Database)
	assert.True(t, session.Request.Config.ReadOnly, "connection-level read-only is the driver's to apply")
	assert.Equal(t, "pw", session.Request.Password)

	res, err := session.Execute(context.Background(), "SELECT * FROM t")
	require.NoError(t, err)
	assert.Len(t, res.Rows.Rows, 3)
}

func TestOpenDatabaseReturnsTheDriversConnectionError(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.OpenErr = errors.New("fakedb: connection refused")
	dbdrivertest.Register(t, fake)

	_, err := OpenDatabase(context.Background(), &asset_entity.Asset{ID: 3},
		&asset_entity.DatabaseConfig{Driver: "fakedb", Host: "10.0.0.5", Port: 7777}, "pw", nil)
	assert.Equal(t, fake.OpenErr, err, "the driver's error reaches the caller as is")
}

func TestOpenDatabaseTransport(t *testing.T) {
	t.Run("direct: the driver dials for itself", func(t *testing.T) {
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{Host: "10.0.0.5", Port: 7777}, nil)
		assert.Nil(t, session.Request.Transport.Dial)
		assert.Nil(t, session.Request.Transport.RemoteFS)
	})

	t.Run("direct to a .local host: dialing bypasses the system resolver", func(t *testing.T) {
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{Host: "db.local", Port: 7777}, nil)
		assert.NotNil(t, session.Request.Transport.Dial)
	})

	t.Run("SOCKS5 proxy", func(t *testing.T) {
		echo := startEcho(t)
		proxyHost, proxyPort := splitHostPort(t, socksdialtest.Start(t, "", ""))
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{
			Host: "10.0.0.5", Port: 7777,
			Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: proxyHost, Port: proxyPort},
		}, nil)
		requireDialEchoes(t, session.Request.Transport.Dial, echo)
	})

	t.Run("proxy chain", func(t *testing.T) {
		echo := startEcho(t)
		proxyHost, proxyPort := splitHostPort(t, socksdialtest.Start(t, "", ""))
		enabled := true
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{
			Host: "10.0.0.5", Port: 7777,
			ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
				Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Host: proxyHost, Port: proxyPort,
			}}},
		}, nil)
		requireDialEchoes(t, session.Request.Transport.Dial, echo)
	})

	t.Run("proxy chain with no layers is a direct connection", func(t *testing.T) {
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{
			Host: "10.0.0.5", Port: 7777, ProxyChain: &asset_entity.ProxyChainConfig{},
		}, nil)
		assert.Nil(t, session.Request.Transport.Dial)
	})

	t.Run("SSH tunnel: dials the configured host and port through the SSH asset", func(t *testing.T) {
		var forwarded string
		srv := startForwardPolicyServer(t, func(target string) bool {
			forwarded = target
			return true
		})
		session := openWithFake(t, &asset_entity.Asset{ID: 3, SSHTunnelID: 9},
			&asset_entity.DatabaseConfig{Host: "db.internal", Port: 7777}, srv.pool(t))

		// 隧道的目标在建隧道时就定了：驱动传什么地址都转发到配置的 host:port。
		conn, err := session.Request.Transport.Dial(context.Background(), "tcp", "ignored:1")
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		requireEcho(t, conn)
		assert.Equal(t, "db.internal:7777", forwarded)
		assert.NotNil(t, session.Request.Transport.RemoteFS, "an SSH asset also offers the host's file system")
	})

	t.Run("the tunnel wins over a proxy", func(t *testing.T) {
		srv := startForwardPolicyServer(t, permitAll)
		session := openWithFake(t, &asset_entity.Asset{ID: 3, SSHTunnelID: 9},
			&asset_entity.DatabaseConfig{
				Host: "db.internal", Port: 7777,
				// 这个代理不存在：连接若走了代理就会失败。
				Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 1},
			}, srv.pool(t))

		conn, err := session.Request.Transport.Dial(context.Background(), "tcp", "db.internal:7777")
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		requireEcho(t, conn)
	})

	t.Run("the deprecated per-config SSH asset still selects the tunnel", func(t *testing.T) {
		srv := startForwardPolicyServer(t, permitAll)
		session := openWithFake(t, &asset_entity.Asset{ID: 3},
			&asset_entity.DatabaseConfig{Host: "db.internal", Port: 7777, SSHAssetID: 9}, srv.pool(t))

		conn, err := session.Request.Transport.Dial(context.Background(), "tcp", "db.internal:7777")
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		requireEcho(t, conn)
	})
}

func startEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 64)
				for {
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					if _, err := conn.Write(buf[:n]); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

// requireDialEchoes 证明 dial 确实能把连接送到 target（经它背后的代理）。
func requireDialEchoes(t *testing.T, dial dbdriver.DialFunc, target string) {
	t.Helper()
	require.NotNil(t, dial)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	requireEcho(t, conn)
}

func TestOpenDatabaseMySQLViaProxyRouted(t *testing.T) {
	Convey("OpenDatabase 的 mysql 代理分支把连接路由到 SOCKS5 代理", t, func() {
		// 假 MySQL 服务:接受连接后立即关闭(驱动会在握手阶段失败,但足以证明路由)。
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		So(err, ShouldBeNil)
		defer func() { _ = ln.Close() }()
		accepted := make(chan struct{}, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = conn.Close()
		}()

		proxyHost, proxyPortStr, err := net.SplitHostPort(socksdialtest.Start(t, "", ""))
		So(err, ShouldBeNil)
		proxyPort, err := strconv.Atoi(proxyPortStr)
		So(err, ShouldBeNil)
		host, portStr, err := net.SplitHostPort(ln.Addr().String())
		So(err, ShouldBeNil)
		port, err := strconv.Atoi(portStr)
		So(err, ShouldBeNil)

		cfg := &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverMySQL, Host: host, Port: port, Username: "u",
			Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: proxyHost, Port: proxyPort},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = OpenDatabase(ctx, &asset_entity.Asset{}, cfg, "pw", nil)
		So(err, ShouldNotBeNil) // 假服务无握手,连接必然失败

		select {
		case <-accepted:
			// 连接经 SOCKS5 代理到达目标,路由正确
		case <-time.After(3 * time.Second):
			t.Fatal("目标服务未收到任何连接:代理分支未生效")
		}
	})
}

// 交给驱动的专属字段按驱动当前的声明整理：没存的取声明的默认值（表单清空了有默认值的字段、
// 驱动升级新增了字段），声明里已经没有的键不交出去（驱动升级删了一个口令类字段，它的密文不该
// 以普通取值的身份交给驱动）。
func TestOpenDatabaseHandsTheDriverItsDeclaredOptions(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.DriverFields = []dbdriver.DriverField{
		{Key: "fetch_size", Type: dbdriver.FieldTypeNumber, Tab: dbdriver.TabAdvanced, Default: 100},
		{Key: "region", Type: dbdriver.FieldTypeText, Tab: dbdriver.TabConnection},
	}
	dbdrivertest.Register(t, fake)

	session, err := OpenDatabase(context.Background(), &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{
		Driver: fake.Desc.ID, Host: "10.0.0.5", Port: 7777,
		DriverOptions: map[string]any{"region": "eu", "retired_token": "ciphertext"},
	}, "pw", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	req := session.(*dbdrivertest.FakeSession).Request
	assert.Equal(t, map[string]any{"fetch_size": 100, "region": "eu"}, req.DriverOptions)
}
