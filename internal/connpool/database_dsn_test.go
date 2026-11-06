package connpool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/connpool/connpooltest"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	_ "github.com/opskat/opskat/internal/dbdriver/mysql"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/opskat/opskat/internal/service/credential_svc"
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
		assert.True(t, session.Request.Transport.Direct, "names resolve on this machine")
		assert.Equal(t, int64(3), session.Request.AssetID, "the session knows its asset, for the host's logs")
	})

	t.Run("direct: dialing by address reaches the address asked for", func(t *testing.T) {
		echo := startEcho(t)
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{Host: "10.0.0.5", Port: 7777}, nil)
		requireDialEchoes(t, session.Request.Transport.DialAddr, echo)
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
		requireDialEchoes(t, session.Request.Transport.DialAddr, echo)
		assert.False(t, session.Request.Transport.Direct, "names resolve at the proxy")
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
		requireDialEchoes(t, session.Request.Transport.DialAddr, echo)
		assert.False(t, session.Request.Transport.Direct, "names resolve along the chain")
	})

	t.Run("proxy chain with no layers is a direct connection", func(t *testing.T) {
		session := openWithFake(t, &asset_entity.Asset{ID: 3}, &asset_entity.DatabaseConfig{
			Host: "10.0.0.5", Port: 7777, ProxyChain: &asset_entity.ProxyChainConfig{},
		}, nil)
		assert.Nil(t, session.Request.Transport.Dial)
		assert.True(t, session.Request.Transport.Direct)
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

	t.Run("SSH tunnel: dialing by address reaches the address asked for, resolved on the SSH host", func(t *testing.T) {
		var mu sync.Mutex
		var forwarded []string
		srv := startForwardPolicyServer(t, func(target string) bool {
			mu.Lock()
			defer mu.Unlock()
			forwarded = append(forwarded, target)
			return true
		})
		session := openWithFake(t, &asset_entity.Asset{ID: 3, SSHTunnelID: 9},
			&asset_entity.DatabaseConfig{Host: "db.internal", Port: 7777}, srv.pool(t))
		assert.False(t, session.Request.Transport.Direct, "names resolve on the SSH host")

		conn, err := session.Request.Transport.DialAddr(context.Background(), "tcp", "node2.internal:7778")
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		requireEcho(t, conn)
		// The asset's own dial still goes to the configured host:port: a built-in
		// driver that dials a resolved ip:port keeps reaching the asset.
		conn, err = session.Request.Transport.Dial(context.Background(), "tcp", "10.9.9.9:1")
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		requireEcho(t, conn)
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{"node2.internal:7778", "db.internal:7777"}, forwarded)
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

// 声明了证书字段组的驱动在资产启用 TLS 时，打开会话就收到 TLS 设置与证书内容：路径由打开会话的
// 这个进程读取，私钥解密后交出；读不到、解不开就打不开，错误点名字段而不含内容；没启用 TLS、
// 或驱动没声明证书字段组（内置驱动都不声明），什么都不交。
func TestOpenDatabaseHandsACertDriverItsTLSMaterial(t *testing.T) {
	credential_svc.SetDefault(credential_svc.New("db-tls-test-key", []byte("0123456789abcdef")))
	core, logs := observer.New(zap.DebugLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })

	certPEM, keyPEM := connpooltest.SelfSignedPEM(t)
	encryptedKey, err := credential_svc.Default().Encrypt(string(keyPEM))
	require.NoError(t, err)
	dir := t.TempDir()
	caFile, certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("ca from file"), 0o600))
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))

	register := func(t *testing.T, group bool) *dbdrivertest.Fake {
		fake := dbdrivertest.NewFake()
		if group {
			fake.Desc.Form = append(fake.Desc.Form, dbdriver.FormTabFields{Tab: dbdriver.TabTLS, Fields: []dbdriver.FormField{dbdriver.FieldTLSCert}})
		}
		dbdrivertest.Register(t, fake)
		return fake
	}
	open := func(cfg *asset_entity.DatabaseConfig) (dbdriver.OpenRequest, error) {
		cfg.Driver, cfg.Host, cfg.Port = "fakedb", "10.0.0.5", 7777
		session, err := OpenDatabase(context.Background(), &asset_entity.Asset{ID: 3}, cfg, "pw", nil)
		if err != nil {
			return dbdriver.OpenRequest{}, err
		}
		t.Cleanup(func() { _ = session.Close() })
		return session.(*dbdrivertest.FakeSession).Request, nil
	}

	t.Run("TLS on with pasted contents: settings, certificates and the decrypted key", func(t *testing.T) {
		register(t, true)
		req, err := open(&asset_entity.DatabaseConfig{
			TLS: true, TLSInsecure: true, TLSServerName: "db.example.com",
			TLSCAPEM: string(certPEM), TLSCertPEM: string(certPEM), TLSKeyPEM: encryptedKey,
		})
		require.NoError(t, err)
		require.NotNil(t, req.TLS)
		assert.Equal(t, dbdriver.TLSMaterial{
			Insecure: true, ServerName: "db.example.com", CA: certPEM, Cert: certPEM, Key: keyPEM,
		}, *req.TLS)
	})

	t.Run("TLS on with file paths: this process reads the files", func(t *testing.T) {
		register(t, true)
		req, err := open(&asset_entity.DatabaseConfig{TLS: true, TLSCAFile: caFile, TLSCertFile: certFile, TLSKeyFile: keyFile})
		require.NoError(t, err)
		require.NotNil(t, req.TLS)
		assert.Equal(t, dbdriver.TLSMaterial{CA: []byte("ca from file"), Cert: certPEM, Key: keyPEM}, *req.TLS)
	})

	t.Run("TLS off: nothing is handed", func(t *testing.T) {
		register(t, true)
		req, err := open(&asset_entity.DatabaseConfig{TLSServerName: "db.example.com", TLSCAPEM: string(certPEM), TLSKeyPEM: encryptedKey})
		require.NoError(t, err)
		assert.Nil(t, req.TLS)
	})

	t.Run("a driver that does not declare the group is handed nothing, TLS on or not", func(t *testing.T) {
		register(t, false)
		req, err := open(&asset_entity.DatabaseConfig{TLS: true, TLSCAPEM: string(certPEM), TLSKeyPEM: encryptedKey})
		require.NoError(t, err)
		assert.Nil(t, req.TLS)
		assert.True(t, req.Config.TLS, "its own tls switch means what it always did")
	})

	t.Run("an unreadable file fails the open, naming the field", func(t *testing.T) {
		register(t, true)
		_, err := open(&asset_entity.DatabaseConfig{TLS: true, TLSCAFile: filepath.Join(dir, "missing.pem")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CA 证书")
	})

	t.Run("a key that does not decrypt fails the open, naming the field without its content", func(t *testing.T) {
		register(t, true)
		_, err := open(&asset_entity.DatabaseConfig{TLS: true, TLSCertPEM: string(certPEM), TLSKeyPEM: string(keyPEM)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "客户端私钥")
		assert.NotContains(t, err.Error(), "PRIVATE KEY")
	})

	t.Run("no certificate content reaches the logs", func(t *testing.T) {
		for _, entry := range logs.All() {
			line := fmt.Sprint(entry.Message, entry.ContextMap())
			assert.NotContains(t, line, "PRIVATE KEY")
			assert.NotContains(t, line, "CERTIFICATE")
			assert.NotContains(t, line, encryptedKey)
		}
		assert.NotEmpty(t, logs.All(), "the opens above were logged")
	})
}
