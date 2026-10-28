package extreg

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
)

// AssetScope 是资产类型这一侧给运行时的作用域：端点只来自 format:"endpoint" 字段里写得出端口
// 的字符串值，连接路径与认证注入按类型的声明。
func TestAssetScope(t *testing.T) {
	at := &extension.AssetTypeDef{Type: "ep", ConfigSchema: map[string]any{"type": "object", "properties": map[string]any{
		"url":    map[string]any{"type": "string", "format": "endpoint"},
		"broker": map[string]any{"type": "string", "format": "endpoint"},
		"v6":     map[string]any{"type": "string", "format": "endpoint"},
		"empty":  map[string]any{"type": "string", "format": "endpoint"},
		"port":   map[string]any{"type": "number", "format": "endpoint"},
		"bare":   map[string]any{"type": "string", "format": "endpoint"},
		"other":  map[string]any{"type": "string"},
	}}}
	config := json.RawMessage(`{"url":"https://ES.internal:9200/base","broker":"10.0.0.5:9092","v6":"[fd00::1]:9300",
		"empty":"","port":9200,"bare":"es.internal","other":"10.0.0.9:22"}`)
	asset := extension.AssetRef{ID: 7, Name: "a", Type: "ep"}

	t.Run("endpoints are the endpoint fields that name a host and a port", func(t *testing.T) {
		scope, err := AssetScope(asset, at, config, AssetConnection{}, nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, []extension.Endpoint{
			{Scheme: "https", Host: "ES.internal", Port: "9200"},
			{Host: "10.0.0.5", Port: "9092"},
			{Host: "fd00::1", Port: "9300"},
		}, scope.Endpoints)
		assert.Equal(t, &asset, scope.Asset)
		assert.Nil(t, scope.Auth, "a type declaring no auth injects nothing")
		assert.Nil(t, scope.Ops, "a tool call is not restricted in kind")
		assert.Nil(t, scope.Tickets, "an extension asset has no ticket source")
	})

	t.Run("no dial is a direct route; a dial is the path, with its TLS and fingerprint", func(t *testing.T) {
		scope, err := AssetScope(asset, at, config, AssetConnection{}, nil)
		require.NoError(t, err)
		require.NotNil(t, scope.Route)
		assert.True(t, scope.Route.Direct)
		assert.NotNil(t, scope.Route.Dial)

		var dialed string
		tlsConfig := &tls.Config{ServerName: "es.internal"}
		scope, err = AssetScope(asset, at, config, AssetConnection{
			Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
				dialed = addr
				return nil, assert.AnError
			},
			TLS: tlsConfig, Fingerprint: "fp",
		}, nil)
		require.NoError(t, err)
		assert.False(t, scope.Route.Direct)
		_, err = scope.Route.Dial(context.Background(), "tcp", "es.internal:9200")
		assert.ErrorIs(t, err, assert.AnError)
		assert.Equal(t, "es.internal:9200", dialed)
		assert.Same(t, tlsConfig, scope.TLS)
		assert.Equal(t, "fp", scope.Fingerprint)
	})

	t.Run("declared auth renders the active group from the credentials", func(t *testing.T) {
		authed := *at
		authed.Auth = &extension.AuthDef{Selector: "kind", Groups: []extension.AuthGroup{
			{When: "bearer", Bindings: []extension.AuthBinding{{In: extension.AuthInHeader, Name: "Authorization", Value: "Bearer {{password}}"}}},
		}}
		creds := map[string]string{"kind": "bearer", "password": "s3cret"}
		scope, err := AssetScope(asset, &authed, config, AssetConnection{}, func(_ context.Context, fields []string) (map[string]string, error) {
			out := map[string]string{}
			for _, f := range fields {
				out[f] = creds[f]
			}
			return out, nil
		})
		require.NoError(t, err)
		values, err := scope.Auth(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []extension.AuthValue{{In: extension.AuthInHeader, Name: "Authorization", Value: "Bearer s3cret"}}, values)

		creds["kind"] = "none"
		values, err = scope.Auth(context.Background())
		require.NoError(t, err)
		assert.Nil(t, values, "a selector value no group names injects nothing")
	})

	t.Run("a config that is not an object is an error", func(t *testing.T) {
		_, err := AssetScope(asset, at, json.RawMessage(`[1]`), AssetConnection{}, nil)
		assert.Error(t, err)
	})
}
