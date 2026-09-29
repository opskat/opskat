package helper

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

const testSecret = "s3cret-token"

// setupGenericDB 用内存 SQLite 跑真实的自定义类型 / 凭据仓储：执行器经 custom_type_svc
// 解析字段值（含密钥解密），测试不绕过这条路径。
func setupGenericDB(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&custom_type_entity.CustomType{}, &asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	oldAsset, oldCredential, oldCustomType := asset_repo.Asset(), credential_repo.Credential(), custom_type_repo.CustomType()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	oldCrypto := credential_svc.Default()
	credential_svc.SetDefault(credential_svc.New("helper-generic-test-key", []byte("helper-generic16")))
	custom_type_svc.CustomType().SetReservedNames(func() []string { return []string{"ssh"} })
	t.Cleanup(func() {
		asset_repo.RegisterAsset(oldAsset)
		credential_repo.RegisterCredential(oldCredential)
		custom_type_repo.RegisterCustomType(oldCustomType)
		credential_svc.SetDefault(oldCrypto)
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return context.Background()
}

// saveHTTPType 保存一个 HTTP 方式的自定义类型：字段 host（必填）、token（密钥，必填）。
func saveHTTPType(t *testing.T, ctx context.Context, slug, baseURL string, auth ...custom_type_entity.AuthBinding) {
	t.Helper()
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: slug, Slug: slug, ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{
			{Name: "host", Required: true},
			{Name: "user"},
			{Name: "token", Secret: true, Required: true},
		},
		HTTP: &custom_type_entity.HTTPConfig{BaseURL: baseURL, Auth: auth},
	}))
}

// genericAsset 构造一台通用资产；values 里的 token 按存储约定加密。
func genericAsset(t *testing.T, slug string, values map[string]string, mutate ...func(*asset_entity.GenericConfig)) *asset_entity.Asset {
	t.Helper()
	cfg := &asset_entity.GenericConfig{CustomType: slug, Values: map[string]asset_entity.GenericValue{}}
	for k, v := range values {
		if k == "token" {
			cipher, err := credential_svc.Default().Encrypt(v)
			require.NoError(t, err)
			v = cipher
		}
		cfg.Values[k] = asset_entity.GenericValue{Value: v}
	}
	for _, m := range mutate {
		m(cfg)
	}
	a := &asset_entity.Asset{ID: 42, Name: slug + "-prod", Type: asset_entity.AssetTypeGeneric}
	require.NoError(t, a.SetGenericConfig(cfg))
	return a
}

// echoServer 把收到的请求原样回显，便于断言注入结果。
type echoServer struct {
	*httptest.Server
	hits atomic.Int32
}

func newEchoServer(t *testing.T, tlsServer bool) *echoServer {
	t.Helper()
	e := &echoServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/new", http.StatusFound)
			return
		case "/missing":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"not found"}`)
			return
		case "/binary":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0, 1, 2})
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Echo", "yes")
		var b strings.Builder
		b.WriteString(r.Method + " " + r.URL.Path + "\n")
		b.WriteString("query=" + r.URL.RawQuery + "\n")
		for _, h := range []string{"Authorization", "X-Api-Key", "X-Caller", "X-Sign", "Content-Type"} {
			if v := r.Header.Get(h); v != "" {
				b.WriteString(h + "=" + v + "\n")
			}
		}
		b.WriteString("body=" + string(body))
		_, _ = io.WriteString(w, b.String())
	})
	if tlsServer {
		e.Server = httptest.NewTLSServer(handler)
	} else {
		e.Server = httptest.NewServer(handler)
	}
	t.Cleanup(e.Close)
	return e
}

func (e *echoServer) host() string {
	return strings.TrimPrefix(strings.TrimPrefix(e.URL, "http://"), "https://")
}

func streamHTTP(t *testing.T, ctx context.Context, asset *asset_entity.Asset, stdin io.Reader, argv ...string) (permission.StreamResult, string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	res, err := StreamGenericOnAsset(ctx, asset, argv, permission.Stdio{Stdin: stdin, Stdout: &stdout, Stderr: &stderr})
	return res, stdout.String(), stderr.String(), err
}

func TestParseHTTPCommand(t *testing.T) {
	cmd, err := ParseHTTPCommand([]string{"get", "/api/search?query=cpu&limit=5", "-H", "X-Caller: a b", "-H", "Accept:json", "-d", "@-", "-i"})
	require.NoError(t, err)
	assert.Equal(t, "GET", cmd.Method)
	assert.Equal(t, "GET /api/search", cmd.PolicySubject(), "the match object is METHOD + path without query")
	assert.True(t, cmd.Include)

	for _, bad := range [][]string{
		{"GET"},
		{"FETCH", "/x"},
		{"GET", "https://evil.example/x"},
		{"GET", "api/x"},
		{"GET", "/api/../admin"},
		{"GET", "/api/%2e%2e/admin"},
		{"GET", "/x#frag"},
		{"GET", "/x", "-H", "no-colon"},
		{"GET", "/x", "-H"},
		{"GET", "/x", "-d", "a", "-d", "b"},
		{"GET", "/x", "--verbose"},
		{"GET", "/x", "extra"},
	} {
		_, err := ParseHTTPCommand(bad)
		assert.Error(t, err, "%q must be rejected", bad)
	}
}

func TestGenericHTTP_InjectsAuthOverridingCallerValues(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "api", "http://{{host}}/base",
		custom_type_entity.AuthBinding{Type: "header", Name: "Authorization", Values: []string{"Bearer {{token}}"}},
		custom_type_entity.AuthBinding{Type: "query", Name: "api_key", Values: []string{"{{token}}"}},
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Sign", Values: []string{"{{hex(hmac_sha256(token, request.method + request.path + request.body))}}"}},
	)
	asset := genericAsset(t, "api", map[string]string{"host": srv.host(), "token": testSecret})

	res, stdout, stderr, err := streamHTTP(t, ctx, asset, nil, "POST", "/items?api_key=caller&x=1",
		"-H", "Authorization: Bearer caller", "-H", "X-Caller: kept", "-d", `{"a":1}`)
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "HTTP 200 OK\n", stderr)
	assert.Contains(t, stdout, "POST /base/items\n")
	assert.Contains(t, stdout, "Authorization=Bearer "+testSecret+"\n", "the binding overrides the caller's same-name header")
	assert.Contains(t, stdout, "X-Caller=kept\n")
	assert.Contains(t, stdout, "api_key="+testSecret)
	assert.NotContains(t, stdout, "api_key=caller", "the binding overrides the caller's same-name query parameter")
	assert.Contains(t, stdout, "x=1")
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(`POST/base/items{"a":1}`))
	assert.Contains(t, stdout, "X-Sign="+hex.EncodeToString(mac.Sum(nil))+"\n", "request.* is the actual request method/path/body")
	assert.Contains(t, stdout, `body={"a":1}`)
	assert.Equal(t, "HTTP 200 OK", res.AuditResult)
	assert.NotContains(t, res.AuditResult, testSecret)
}

func TestGenericHTTP_BasicAuth(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "basic", "http://{{host}}",
		custom_type_entity.AuthBinding{Type: "basic", Values: []string{"{{user}}", "{{token}}"}})
	asset := genericAsset(t, "basic", map[string]string{"host": srv.host(), "user": "admin", "token": testSecret})

	_, stdout, _, err := streamHTTP(t, ctx, asset, nil, "GET", "/x")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", testSecret)
	assert.Contains(t, stdout, "Authorization="+req.Header.Get("Authorization"))
}

func TestGenericHTTP_BodySources(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "body", "http://{{host}}")
	asset := genericAsset(t, "body", map[string]string{"host": srv.host(), "token": testSecret})

	_, stdout, _, err := streamHTTP(t, ctx, asset, nil, "PUT", "/x", "-d", "@not-a-file-literal-is-only-with-@")
	require.Error(t, err, "a missing @file fails before any request")
	assert.Empty(t, stdout)
	assert.Equal(t, int32(0), srv.hits.Load())

	file := filepath.Join(t.TempDir(), "body.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"from":"file"}`), 0o600))
	_, stdout, _, err = streamHTTP(t, ctx, asset, nil, "PUT", "/x", "-d", "@"+file)
	require.NoError(t, err)
	assert.Contains(t, stdout, `body={"from":"file"}`)

	_, stdout, _, err = streamHTTP(t, ctx, asset, strings.NewReader("from stdin"), "PUT", "/x", "-d", "@-")
	require.NoError(t, err)
	assert.Contains(t, stdout, "body=from stdin")

	_, stdout, _, err = streamHTTP(t, ctx, asset, nil, "PUT", "/x", "-d", "{{token}}")
	require.NoError(t, err)
	assert.Contains(t, stdout, "body={{token}}", "exec input is literal; templates are never rendered in it")
}

func TestGenericHTTP_IncludeAndExitCodes(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "codes", "http://{{host}}")
	asset := genericAsset(t, "codes", map[string]string{"host": srv.host(), "token": testSecret})

	res, stdout, stderr, err := streamHTTP(t, ctx, asset, nil, "GET", "/x", "-i")
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "HTTP 200 OK\n", stderr)
	assert.True(t, strings.HasPrefix(stdout, "HTTP 200 OK\n"), "with -i the status line leads stdout: %q", stdout)
	assert.Contains(t, stdout, "X-Echo: yes\n")
	assert.Contains(t, stdout, "\n\nGET /x\n", "headers, a blank line, then the body")

	res, stdout, stderr, err = streamHTTP(t, ctx, asset, nil, "GET", "/missing")
	require.NoError(t, err)
	assert.Equal(t, 1, res.ExitCode, "non-2xx exits 1")
	assert.Equal(t, `{"message":"not found"}`, stdout, "the body is still written")
	assert.Equal(t, "HTTP 404 Not Found\n", stderr)
}

func TestGenericHTTP_Redirects(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "redir", "http://{{host}}",
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Api-Key", Values: []string{"{{token}}"}})
	asset := genericAsset(t, "redir", map[string]string{"host": srv.host(), "token": testSecret})

	_, stdout, _, err := streamHTTP(t, ctx, asset, nil, "GET", "/old")
	require.NoError(t, err)
	assert.Contains(t, stdout, "GET /new\n")
	assert.Contains(t, stdout, "X-Api-Key="+testSecret, "same-origin redirects keep the injected auth")

	var otherHits atomic.Int32
	var otherAuth atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		otherAuth.Store(r.Header.Get("X-Api-Key"))
	}))
	defer other.Close()
	cross := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer cross.Close()
	crossAsset := genericAsset(t, "redir", map[string]string{"host": strings.TrimPrefix(cross.URL, "http://"), "token": testSecret})
	_, stdout, _, err = streamHTTP(t, ctx, crossAsset, nil, "GET", "/go")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cross-origin redirect")
	assert.Empty(t, stdout)
	assert.Equal(t, int32(0), otherHits.Load(), "auth must never be sent to another origin")
}

func TestGenericHTTP_NoRequestWhenValuesMissingOrUndecryptable(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "strict", "http://{{host}}",
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Api-Key", Values: []string{"{{token}}"}})

	missing := genericAsset(t, "strict", map[string]string{"host": srv.host()})
	_, stdout, _, err := streamHTTP(t, ctx, missing, nil, "GET", "/x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
	assert.Empty(t, stdout)
	assert.Error(t, PrecheckGeneric(ctx, missing), "missing values fail before approval")

	broken := genericAsset(t, "strict", map[string]string{"host": srv.host()}, func(c *asset_entity.GenericConfig) {
		c.Values["token"] = asset_entity.GenericValue{Value: "not-a-ciphertext"}
	})
	_, _, _, err = streamHTTP(t, ctx, broken, nil, "GET", "/x")
	require.Error(t, err)
	assert.Equal(t, int32(0), srv.hits.Load(), "no request — in particular no unauthenticated one")

	absolute := genericAsset(t, "strict", map[string]string{"host": srv.host(), "token": testSecret})
	_, _, _, err = streamHTTP(t, ctx, absolute, nil, "GET", srv.URL+"/x")
	require.Error(t, err)
	assert.Equal(t, int32(0), srv.hits.Load())
}

func TestGenericHTTP_ConnectionErrorsSurfaceWithoutInjectedValues(t *testing.T) {
	ctx := setupGenericDB(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	dead := l.Addr().String()
	require.NoError(t, l.Close())
	saveHTTPType(t, ctx, "dead", "http://{{host}}",
		custom_type_entity.AuthBinding{Type: "query", Name: "api_key", Values: []string{"{{token}}"}})
	asset := genericAsset(t, "dead", map[string]string{"host": dead, "token": testSecret})

	_, stdout, _, err := streamHTTP(t, ctx, asset, nil, "GET", "/x?y=1")
	require.Error(t, err)
	assert.Empty(t, stdout)
	assert.Contains(t, err.Error(), "connection refused")
	assert.NotContains(t, err.Error(), testSecret, "net/http puts the full URL (with the injected query) into its errors")

	tunneled := genericAsset(t, "dead", map[string]string{"host": dead, "token": testSecret})
	tunneled.SSHTunnelID = 7
	_, _, _, err = streamHTTP(t, ctx, tunneled, nil, "GET", "/x")
	assert.ErrorContains(t, err, "SSH", "a tunnel without an SSH pool errors instead of dialing directly")
}

func TestGenericHTTP_TLSFromAssetConfig(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, true)
	saveHTTPType(t, ctx, "tls", "https://{{host}}")

	plain := genericAsset(t, "tls", map[string]string{"host": srv.host(), "token": testSecret})
	_, _, _, err := streamHTTP(t, ctx, plain, nil, "GET", "/x")
	require.Error(t, err, "an untrusted certificate is an error, never a silent insecure retry")
	assert.Contains(t, err.Error(), "certificate")

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600))
	trusted := genericAsset(t, "tls", map[string]string{"host": srv.host(), "token": testSecret}, func(c *asset_entity.GenericConfig) {
		c.TLSCAFile = caFile
	})
	res, stdout, _, err := streamHTTP(t, ctx, trusted, nil, "GET", "/x")
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, stdout, "GET /x")
}

func TestGenericHTTP_AIExecOutput(t *testing.T) {
	ctx := setupGenericDB(t)
	srv := newEchoServer(t, false)
	saveHTTPType(t, ctx, "ai", "http://{{host}}",
		custom_type_entity.AuthBinding{Type: "header", Name: "X-Api-Key", Values: []string{"{{token}}"}})
	asset := genericAsset(t, "ai", map[string]string{"host": srv.host(), "token": testSecret})

	out, err := ExecGenericOnAsset(ctx, asset, `GET /api/search?query=cpu -H 'X-Caller: two words'`, "")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "HTTP 200 OK\n\nGET /api/search\n"), "status line, blank line, body: %q", out)
	assert.Contains(t, out, "X-Caller=two words", "the command string is split with shell-word rules")

	out, err = ExecGenericOnAsset(ctx, asset, "GET /missing", "")
	require.NoError(t, err, "a non-2xx response is a result for the model, not a tool error")
	assert.Equal(t, "HTTP 404 Not Found\n\n{\"message\":\"not found\"}", out)

	out, err = ExecGenericOnAsset(ctx, asset, "GET /binary", "")
	require.NoError(t, err)
	assert.Equal(t, "HTTP 200 OK\n\n(binary response, 7 bytes, image/png)", out)

	_, err = ExecGenericOnAsset(ctx, asset, "POST /x -d @-", "")
	assert.ErrorContains(t, err, "stdin")
	hostsFile := filepath.Join(t.TempDir(), "local.txt")
	require.NoError(t, os.WriteFile(hostsFile, []byte("local secret"), 0o600))
	_, err = ExecGenericOnAsset(ctx, asset, "GET /x -d @"+hostsFile, "")
	assert.ErrorContains(t, err, "opsctl", "AI exec must not read local files past the local-tool approval gate")
	assert.Equal(t, int32(3), srv.hits.Load(), "no request is sent for a rejected local body source")

	canonical, err := CanonicalizeGenericCommand(asset, "get /api/dashboards/uid/abc?x=1 -H 'A: b'")
	require.NoError(t, err)
	assert.Equal(t, "GET /api/dashboards/uid/abc", canonical)

	detail, err := DescribeGenericCommand(ctx, asset, "GET /api/search?query=cpu")
	require.NoError(t, err)
	assert.Equal(t, "HTTP request: GET "+srv.URL+"/api/search?query=cpu", detail)
	assert.NotContains(t, detail, testSecret)
}

func TestGenericExec_CommandModeNotSupportedYet(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "profile"}},
		Command: &custom_type_entity.CommandConfig{Template: "aws --profile {{profile}}"},
	}))
	asset := genericAsset(t, "cli", map[string]string{"profile": "prod"})

	_, err := CanonicalizeGenericCommand(asset, "s3 ls")
	assert.ErrorContains(t, err, "not supported yet")
	_, err = ExecGenericOnAsset(ctx, asset, "s3 ls", "")
	assert.ErrorContains(t, err, "not supported yet")
	_, _, _, err = streamHTTP(t, ctx, asset, nil, "s3", "ls")
	assert.ErrorContains(t, err, "not supported yet")
}
