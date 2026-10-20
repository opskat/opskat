package asset_entity

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 驱动看不到的字段：口令与凭据由宿主解析后单独交给驱动，代理由宿主解析成传输层。
var hostOnlyDatabaseFields = []string{"password", "credential_id", "proxy", "proxy_chain", "driver_options"}

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
		DriverOptions: cfg.DriverOptions}

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
