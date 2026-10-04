package helper

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// authServer answers GET / with 204 when X-Api-Key carries testSecret, 401 otherwise.
func authServer(t *testing.T) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var seen atomic.Value
	seen.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Method + " " + r.URL.Path)
		if r.Header.Get("X-Api-Key") != testSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// closedLocalAddr returns a local address nobody listens on.
func closedLocalAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func connTestJSON(t *testing.T, in map[string]any) string {
	t.Helper()
	b, err := json.Marshal(in)
	require.NoError(t, err)
	return string(b)
}

// TestGenericConnTest_EnglishCtxProducesEnglishRouteAndFailureText covers O1: the route
// label and the auth-failure text must follow the ctx language (policy.PolicyMsg/PolicyFmt),
// not a hardcoded Chinese string, per spec "通用资产"「测试连接」+ the desktop binder's
// i18n.Ctx (internal/app/system/asset.go:104) already carrying the user's language.
func TestGenericConnTest_EnglishCtxProducesEnglishRouteAndFailureText(t *testing.T) {
	ctx := setupGenericDB(t)
	srv, _ := authServer(t)
	saveHTTPType(t, ctx, "probe", "http://{{host}}/",
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Api-Key", Values: []string{"{{token}}"}})
	host := strings.TrimPrefix(srv.URL, "http://")
	enCtx := aictx.WithPolicyLang(ctx, "en")

	detail, err := ProbeGenericConnection(enCtx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "values": map[string]any{"host": host, "token": testSecret},
	}), "")
	require.NoError(t, err)
	assert.Contains(t, detail, "direct connection", "en ctx names the route in English")
	assert.NotContainsf(t, detail, "直连", "detail %q must not leak Chinese under an English ctx", detail)

	_, err = ProbeGenericConnection(enCtx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "values": map[string]any{"host": host, "token": "wrong"},
	}), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication failed")
	assert.NotContainsf(t, err.Error(), "认证失败", "error %q must not leak Chinese under an English ctx", err.Error())
}

func TestGenericConnTest_GetsBaseURLAndReportsStatus(t *testing.T) {
	ctx := aictx.WithPolicyLang(setupGenericDB(t), "zh")
	srv, seen := authServer(t)
	saveHTTPType(t, ctx, "probe", "http://{{host}}/",
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Api-Key", Values: []string{"{{token}}"}})
	host := strings.TrimPrefix(srv.URL, "http://")

	detail, err := ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "values": map[string]any{"host": host, "token": testSecret},
	}), "")
	require.NoError(t, err)
	assert.Equal(t, "GET /", seen.Load(), "the test is a GET of the Base URL itself")
	assert.Contains(t, detail, "HTTP 204 No Content")
	assert.Contains(t, detail, "直连", "the detail names the route the request took")

	_, err = ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "values": map[string]any{"host": host, "token": "wrong"},
	}), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "认证失败", "401 / 403 is reported as an authentication failure")
	assert.Contains(t, err.Error(), "401")
	assert.NotContains(t, err.Error(), "wrong")

	_, err = ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "values": map[string]any{"host": host},
	}), "")
	require.ErrorContains(t, err, "token", "a missing required value fails before any request")
}

// Editing an existing asset: secret fields the form did not touch are left out of the
// input and reuse the stored value; a secret the user typed replaces it.
func TestGenericConnTest_EditReusesUnchangedStoredSecrets(t *testing.T) {
	ctx := setupGenericDB(t)
	srv, _ := authServer(t)
	saveHTTPType(t, ctx, "probe", "http://{{host}}",
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Api-Key", Values: []string{"{{token}}"}})
	host := strings.TrimPrefix(srv.URL, "http://")
	stored := genericAsset(t, "probe", map[string]string{"host": "stale.invalid", "token": testSecret})
	require.NoError(t, asset_repo.Asset().Create(ctx, stored))

	detail, err := ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"asset_id": stored.ID, "custom_type": "probe", "values": map[string]any{"host": host},
	}), "")
	require.NoError(t, err, "the unchanged token comes from the stored asset")
	assert.Contains(t, detail, "HTTP 204")

	_, err = ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"asset_id": stored.ID, "custom_type": "probe", "values": map[string]any{"host": host, "token": "typed-new"},
	}), "")
	assert.ErrorContains(t, err, "401", "a secret typed in the form replaces the stored one")
}

func TestGenericConnTest_ConnectionErrorsSurfaceAndCommandTypesAreRejected(t *testing.T) {
	ctx := setupGenericDB(t)
	saveHTTPType(t, ctx, "probe", "http://{{host}}")

	_, err := ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "values": map[string]any{"host": closedLocalAddr(t), "token": testSecret},
	}), "")
	assert.ErrorContains(t, err, "connection refused")

	_, err = ProbeGenericConnection(ctx, connTestJSON(t, map[string]any{
		"custom_type": "probe", "ssh_tunnel_id": 9, "values": map[string]any{"host": "10.0.0.1", "token": testSecret},
	}), "")
	assert.ErrorContains(t, err, "SSH", "a tunnel that cannot be used is an error, never a direct connection")

	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "profile"}},
		Command: &custom_type_entity.CommandConfig{Template: "aws"},
	}))
	_, err = ProbeGenericConnection(context.Background(), connTestJSON(t, map[string]any{"custom_type": "cli"}), "")
	assert.ErrorContains(t, err, "HTTP")
}
