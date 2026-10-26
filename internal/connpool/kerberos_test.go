package connpool

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/kerberos/kerberostest"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

const krbSPN = "oracle/db.example.com"

// kerberosEnv 是一个假 KDC 加一个（按需）声明了 Kerberos 字段组的假驱动。
type kerberosEnv struct {
	kdc *kerberostest.KDC
	enc string // 加密后的用户口令
}

func newKerberosEnv(t *testing.T, group bool) *kerberosEnv {
	t.Helper()
	credential_svc.SetDefault(credential_svc.New("krb-test-key", []byte("0123456789abcdef")))
	kdc := kerberostest.New(t, "EXAMPLE.COM")
	kdc.AddUser("alice", "alice-secret")
	kdc.AddService(krbSPN)
	enc, err := credential_svc.Default().Encrypt("alice-secret")
	require.NoError(t, err)

	fake := dbdrivertest.NewFake()
	if group {
		fake.Desc.Form = append(fake.Desc.Form, dbdriver.FormTabFields{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{dbdriver.FieldKerberos}})
	}
	dbdrivertest.Register(t, fake)
	return &kerberosEnv{kdc: kdc, enc: enc}
}

func (e *kerberosEnv) config() *asset_entity.DatabaseConfig {
	return &asset_entity.DatabaseConfig{
		Driver: "fakedb", Host: "10.0.0.5", Port: 1521, Username: "ignored",
		AuthMethod: asset_entity.AuthMethodKerberos, KrbPrincipal: "alice@EXAMPLE.COM",
		KrbPassword: e.enc, KrbKDC: e.kdc.Addr(),
	}
}

func openKerberos(t *testing.T, id int64, cfg *asset_entity.DatabaseConfig) (dbdriver.OpenRequest, error) {
	t.Helper()
	session, err := OpenDatabase(context.Background(), &asset_entity.Asset{ID: id}, cfg, "asset-password", nil)
	if err != nil {
		return dbdriver.OpenRequest{}, err
	}
	t.Cleanup(func() { _ = session.Close() })
	return session.(*dbdrivertest.FakeSession).Request, nil
}

func requireTicket(t *testing.T, kdc *kerberostest.KDC, req dbdriver.OpenRequest) {
	t.Helper()
	require.NotNil(t, req.Kerberos)
	ap, err := req.Kerberos.Ticket(context.Background(), krbSPN)
	require.NoError(t, err)
	got, err := kdc.VerifyAPReq(ap)
	require.NoError(t, err)
	assert.Equal(t, kerberostest.APReq{Client: "alice@EXAMPLE.COM", SPN: krbSPN}, got)
}

// 声明了 Kerberos 字段组的驱动在资产认证方式为 Kerberos 时收到票据来源；驱动要票据时，宿主用资产里
// 解密后的口令向 KDC 取。
func TestOpenDatabaseHandsAKerberosDriverATicketSource(t *testing.T) {
	env := newKerberosEnv(t, true)
	req, err := openKerberos(t, 101, env.config())
	require.NoError(t, err)
	assert.Equal(t, "alice@EXAMPLE.COM", req.Kerberos.Principal)
	assert.Empty(t, req.Kerberos.SPN)
	requireTicket(t, env.kdc, req)
}

func TestOpenDatabaseKerberosKeytabFile(t *testing.T) {
	env := newKerberosEnv(t, true)
	cfg := env.config()
	cfg.KrbPassword, cfg.KrbKeytabFile = "", env.kdc.WriteKeytab(t, "alice")
	req, err := openKerberos(t, 102, cfg)
	require.NoError(t, err)
	requireTicket(t, env.kdc, req)
}

// 票据来源走资产的途径：经 SOCKS5 代理才连得到 KDC，代理凭据不对就取不到，而不是绕开代理直连。
func TestOpenDatabaseKerberosTicketTravelsTheAssetRoute(t *testing.T) {
	env := newKerberosEnv(t, true)
	host, portStr, err := net.SplitHostPort(socksdialtest.Start(t, "proxy-user", "proxy-pass"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	good := env.config()
	good.Proxy = &asset_entity.ProxyConfig{Type: "socks5", Host: host, Port: port, Username: "proxy-user", Password: "proxy-pass"}
	req, err := openKerberos(t, 103, good)
	require.NoError(t, err)
	requireTicket(t, env.kdc, req)

	bad := env.config()
	bad.Proxy = &asset_entity.ProxyConfig{Type: "socks5", Host: host, Port: port, Username: "proxy-user", Password: "wrong"}
	req, err = openKerberos(t, 104, bad)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = req.Kerberos.Ticket(ctx, krbSPN)
	require.Error(t, err)
}

// 同一资产的多个会话共用一份缓存（KDC 只被问一次）；设置改了就作废重取。
func TestOpenDatabaseKerberosSharesTheAssetCacheAndInvalidatesOnChange(t *testing.T) {
	env := newKerberosEnv(t, true)
	a, err := openKerberos(t, 105, env.config())
	require.NoError(t, err)
	b, err := openKerberos(t, 105, env.config())
	require.NoError(t, err)
	requireTicket(t, env.kdc, a)
	firstLogin := env.kdc.ASRequests() // 含预认证往返，故不写死次数
	requireTicket(t, env.kdc, b)
	assert.Equal(t, firstLogin, env.kdc.ASRequests(), "the second instance reuses the cached ticket")

	env.kdc.AddUser("bob", "bob-secret")
	enc, err := credential_svc.Default().Encrypt("bob-secret")
	require.NoError(t, err)
	changed := env.config()
	changed.KrbPrincipal, changed.KrbPassword = "bob@EXAMPLE.COM", enc
	c, err := openKerberos(t, 105, changed)
	require.NoError(t, err)
	ap, err := c.Kerberos.Ticket(context.Background(), krbSPN)
	require.NoError(t, err)
	got, err := env.kdc.VerifyAPReq(ap)
	require.NoError(t, err)
	assert.Equal(t, "bob@EXAMPLE.COM", got.Client)
	assert.Greater(t, env.kdc.ASRequests(), firstLogin, "changed settings log in again")
}

// 没有资产 ID 的登录（桌面端测试连接）不进共用的按资产缓存：同样的设置也每次重新登录，
// 不同资产的测试连接之间不会互相复用或挤掉票据。
func TestOpenDatabaseKerberosWithoutAssetIDLogsInAfresh(t *testing.T) {
	env := newKerberosEnv(t, true)
	loginCount := func(id int64) int {
		before := env.kdc.ASRequests()
		req, err := openKerberos(t, id, env.config())
		require.NoError(t, err)
		requireTicket(t, env.kdc, req)
		return env.kdc.ASRequests() - before
	}
	first := loginCount(0)
	require.Positive(t, first)
	assert.Equal(t, first, loginCount(0), "the second test connection logs in again instead of reusing a shared entry")
}

func TestOpenDatabaseKerberosPinnedSPNLosesItsRealm(t *testing.T) {
	env := newKerberosEnv(t, true)
	cfg := env.config()
	cfg.KrbSPN = krbSPN + "@EXAMPLE.COM"
	req, err := openKerberos(t, 106, cfg)
	require.NoError(t, err)
	assert.Equal(t, krbSPN, req.Kerberos.SPN)

	cfg.KrbSPN = krbSPN + "@OTHER.COM"
	_, err = openKerberos(t, 106, cfg)
	require.ErrorContains(t, err, "krb_spn")
}

func TestOpenDatabaseKerberosErrorsNameTheFieldWithoutSecrets(t *testing.T) {
	env := newKerberosEnv(t, true)
	cfg := env.config()
	cfg.KrbPassword = "not-encrypted-at-all"
	_, err := openKerberos(t, 107, cfg)
	require.ErrorContains(t, err, "krb_password")
	assert.NotContains(t, err.Error(), "not-encrypted-at-all")
}

// 驱动没声明字段组（内置驱动都不声明）、或资产用口令认证：什么都不交，行为照旧。
func TestOpenDatabaseWithoutKerberosHandsNothing(t *testing.T) {
	t.Run("driver without the group", func(t *testing.T) {
		env := newKerberosEnv(t, false)
		req, err := openKerberos(t, 108, env.config())
		require.NoError(t, err)
		assert.Nil(t, req.Kerberos)
		assert.Equal(t, "asset-password", req.Password)
	})
	t.Run("password authentication", func(t *testing.T) {
		env := newKerberosEnv(t, true)
		cfg := env.config()
		cfg.AuthMethod = ""
		req, err := openKerberos(t, 109, cfg)
		require.NoError(t, err)
		assert.Nil(t, req.Kerberos)
		assert.Equal(t, "asset-password", req.Password)
	})
}
