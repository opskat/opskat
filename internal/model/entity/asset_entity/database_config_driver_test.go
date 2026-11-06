package asset_entity

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 驱动看不到的字段：口令与凭据由宿主解析后单独交给驱动，代理由宿主解析成传输层，证书字段组由宿主
// 读成内容、解密私钥后放进 OpenRequest.TLS，Kerberos 的主体与凭据留在宿主、只交出票据（认证方式
// auth_method 与 tls 开关一样驱动看得到）。
var hostOnlyDatabaseFields = []string{"password", "credential_id", "proxy", "proxy_chain", "driver_options",
	"tls_insecure", "tls_server_name", "tls_ca_file", "tls_cert_file", "tls_key_file", "tls_ca_pem", "tls_cert_pem", "tls_key_pem",
	"krb_principal", "krb_password", "krb_keytab_file", "krb_kdc", "krb_spn"}

// filledDatabaseConfig 给 DatabaseConfig 的每个字段填上非零值，新增字段也会被填上。
func filledDatabaseConfig(t *testing.T) *DatabaseConfig {
	t.Helper()
	cfg := &DatabaseConfig{}
	v := reflect.ValueOf(cfg).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(v.Type().Field(i).Name)
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(i + 1))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Map:
			f.Set(reflect.MakeMap(f.Type()))
			f.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf("v"))
		default:
			t.Fatalf("filledDatabaseConfig: unhandled field %s of kind %s", v.Type().Field(i).Name, f.Kind())
		}
	}
	return cfg
}

func jsonObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestDriverConfigCarriesEveryFieldButCredentialsAndProxies(t *testing.T) {
	cfg := filledDatabaseConfig(t)

	want := jsonObject(t, cfg)
	for _, k := range hostOnlyDatabaseFields {
		require.Contains(t, want, k)
		delete(want, k)
	}
	assert.Equal(t, want, jsonObject(t, cfg.DriverConfig()))
}

func TestSetDriverConfigWritesDriverFieldsAndKeepsCredentialsAndProxies(t *testing.T) {
	cfg := filledDatabaseConfig(t)
	keep := DatabaseConfig{Password: cfg.Password, CredentialID: cfg.CredentialID, Proxy: cfg.Proxy, ProxyChain: cfg.ProxyChain,
		DriverOptions: cfg.DriverOptions,
		TLSInsecure:   cfg.TLSInsecure, TLSServerName: cfg.TLSServerName,
		TLSCAFile: cfg.TLSCAFile, TLSCertFile: cfg.TLSCertFile, TLSKeyFile: cfg.TLSKeyFile,
		TLSCAPEM: cfg.TLSCAPEM, TLSCertPEM: cfg.TLSCertPEM, TLSKeyPEM: cfg.TLSKeyPEM,
		KrbPrincipal: cfg.KrbPrincipal, KrbPassword: cfg.KrbPassword,
		KrbKeytabFile: cfg.KrbKeytabFile, KrbKDC: cfg.KrbKDC, KrbSPN: cfg.KrbSPN}

	cfg.SetDriverConfig((&DatabaseConfig{}).DriverConfig())

	assert.Equal(t, keep, *cfg)
}

// 驱动的配置校验只看到到达途径的摘要（Route）：SQLite 据它拒绝有层的代理链，空代理链不算。
func TestValidateDatabaseSQLiteRejectsProxyChainWithLayers(t *testing.T) {
	a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
	cfg := &DatabaseConfig{Driver: DriverSQLite, Path: "/tmp/x.db", ProxyChain: &ProxyChainConfig{
		Layers: []ProxyChainLayer{{Type: ProxyChainLayerSOCKS5, Host: "proxy.internal", Port: 1080}},
	}}
	require.NoError(t, a.SetDatabaseConfig(cfg))
	require.ErrorContains(t, a.Validate(), "代理链")

	cfg.ProxyChain = &ProxyChainConfig{}
	require.NoError(t, a.SetDatabaseConfig(cfg))
	require.NoError(t, a.Validate())
}

// Kerberos 设置存在资产配置里，驱动不可用时原样保留；认证方式为 Kerberos 时，保存前就拒绝
// 缺主体 realm、缺 KDC、缺凭据的配置并点名字段。
func TestValidateDatabaseKerberos(t *testing.T) {
	valid := func() *DatabaseConfig {
		// 未注册的驱动（如未加载的扩展驱动）没有驱动规则，这里只看 Kerberos 字段本身的规则
		return &DatabaseConfig{Driver: "krbdb", Host: "db.example.com", Port: 1521, AuthMethod: AuthMethodKerberos,
			KrbPrincipal: "alice@EXAMPLE.COM", KrbPassword: "enc", KrbKDC: "kdc.example.com"}
	}
	validate := func(cfg *DatabaseConfig) error {
		a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
		require.NoError(t, a.SetDatabaseConfig(cfg))
		return a.Validate()
	}
	require.NoError(t, validate(valid()))

	cases := map[string]struct {
		mutate func(*DatabaseConfig)
		field  string
	}{
		"principal without realm": {func(c *DatabaseConfig) { c.KrbPrincipal = "alice" }, "krb_principal"},
		"principal empty realm":   {func(c *DatabaseConfig) { c.KrbPrincipal = "alice@" }, "krb_principal"},
		"no kdc":                  {func(c *DatabaseConfig) { c.KrbKDC = " , " }, "krb_kdc"},
		"no credential":           {func(c *DatabaseConfig) { c.KrbPassword = "" }, "krb_password"},
		"spn of another realm":    {func(c *DatabaseConfig) { c.KrbSPN = "oracle/db.example.com@OTHER.COM" }, "krb_spn"},
		"unknown auth method":     {func(c *DatabaseConfig) { c.AuthMethod = "ldap" }, "auth_method"},
		// 宿主只放行 service/host 形式的服务主体：固定成别的形式就永远登录不了，保存时就拒绝
		"spn without host":    {func(c *DatabaseConfig) { c.KrbSPN = "oracle" }, "krb_spn"},
		"spn with two hosts":  {func(c *DatabaseConfig) { c.KrbSPN = "oracle/db/x" }, "krb_spn"},
		"spn without service": {func(c *DatabaseConfig) { c.KrbSPN = "/db.example.com" }, "krb_spn"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(cfg)
			require.ErrorContains(t, validate(cfg), tc.field)
		})
	}

	t.Run("keytab instead of password, spn of the principal's realm", func(t *testing.T) {
		cfg := valid()
		cfg.KrbPassword, cfg.KrbKeytabFile, cfg.KrbSPN = "", "/etc/alice.keytab", "oracle/db.example.com@EXAMPLE.COM"
		require.NoError(t, validate(cfg))
		spn, err := cfg.KerberosPinnedSPN()
		require.NoError(t, err)
		assert.Equal(t, "oracle/db.example.com", spn)
	})

	// opsctl / AI 写的 krb_kdc 与桌面端表单一样可用逗号、分号或空白分隔
	t.Run("kdc list accepts the separators the desktop form accepts", func(t *testing.T) {
		cfg := valid()
		cfg.KrbKDC = "kdc1.example.com kdc2.example.com:88;kdc3.example.com,\tkdc4"
		assert.Equal(t, []string{"kdc1.example.com", "kdc2.example.com:88", "kdc3.example.com", "kdc4"}, cfg.KerberosKDCs())
	})

	t.Run("password authentication ignores stale kerberos fields", func(t *testing.T) {
		cfg := valid()
		cfg.AuthMethod, cfg.KrbPrincipal = "", "alice"
		require.NoError(t, validate(cfg))
	})
}
