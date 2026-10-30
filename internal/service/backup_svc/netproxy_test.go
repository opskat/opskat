package backup_svc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"
)

// 调用 netproxy.Set 的测试改了进程级状态，不能 t.Parallel。
func useNetworkProxy(t *testing.T, s netproxy.Settings) {
	t.Helper()
	prev := netproxy.Get()
	netproxy.Set(s)
	t.Cleanup(func() { netproxy.Set(prev) })
}

// proxyScenario 起本地服务，并让非回环主机名 host 只能经录制代理到达它。
func proxyScenario(t *testing.T, host string, mode netproxy.Mode, h http.Handler) *netproxytest.Proxy {
	t.Helper()
	proxy := netproxytest.NewRecordingProxy(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	proxy.Forward(host, strings.TrimPrefix(srv.URL, "http://"))
	s := proxy.Settings(netproxy.SchemeHTTP)
	s.Mode = mode
	useNetworkProxy(t, s)
	return proxy
}

func TestGitHubBackupFollowsNetworkProxy(t *testing.T) {
	const host = "github.backup.test"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/login/device/code":
			writeJSON(w, map[string]any{"device_code": "dc", "user_code": "UC", "verification_uri": "https://x", "expires_in": 900, "interval": 5})
		case r.URL.Path == "/login/oauth/access_token":
			writeJSON(w, map[string]any{"access_token": "tok"})
		case r.URL.Path == "/user":
			writeJSON(w, map[string]any{"login": "u"})
		case r.URL.Path == "/gists" && r.Method == http.MethodGet:
			writeJSON(w, []any{})
		case strings.HasPrefix(r.URL.Path, "/gists/") && r.Method == http.MethodGet:
			writeJSON(w, map[string]any{"files": map[string]any{"opskat-backup.encrypted.json": map[string]any{"content": "c"}}})
		default:
			writeJSON(w, map[string]any{"id": "g1"})
		}
	})
	calls := map[string]func() error{
		"device flow start": func() error { _, err := StartDeviceFlow(); return err },
		"device flow poll":  func() error { _, _, _, err := pollOnce("dc"); return err },
		"user":              func() error { _, err := GetGitHubUser("tok"); return err },
		"gist create":       func() error { _, err := CreateOrUpdateGist("tok", "", []byte("x")); return err },
		"gist update":       func() error { _, err := CreateOrUpdateGist("tok", "g1", []byte("x")); return err },
		"gist read":         func() error { _, err := GetGistContent("tok", "g1"); return err },
		"gist list":         func() error { _, err := ListBackupGists("tok"); return err },
	}
	for name, call := range calls {
		for _, mode := range []netproxy.Mode{netproxy.ModeCustom, netproxy.ModeDirect} {
			t.Run(name+" "+string(mode), func(t *testing.T) {
				proxy := proxyScenario(t, host, mode, handler)
				prevBase, prevAPI := githubBaseURL, githubAPIBaseURL
				githubBaseURL, githubAPIBaseURL = "http://"+host, "http://"+host
				t.Cleanup(func() { githubBaseURL, githubAPIBaseURL = prevBase, prevAPI })

				err := call()
				if mode == netproxy.ModeCustom {
					require.NoError(t, err)
					require.NotEmpty(t, proxy.Requests())
					assert.Equal(t, host+":80", proxy.Requests()[0].Target)
				} else {
					assert.Empty(t, proxy.Requests())
				}
			})
		}
	}
}

func TestWebDAVBackupFollowsNetworkProxy(t *testing.T) {
	const host = "dav.backup.test"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	cfg := WebDAVConfig{URL: "http://" + host + "/dav/opskat/"}

	t.Run("custom proxy carries the request", func(t *testing.T) {
		proxy := proxyScenario(t, host, netproxy.ModeCustom, handler)
		_, err := CreateOrUpdateWebDAVBackup(cfg, []byte("payload"))
		require.NoError(t, err)
		require.NotEmpty(t, proxy.Requests())
		for _, r := range proxy.Requests() {
			assert.Equal(t, host+":80", r.Target)
		}
	})
	t.Run("direct never touches the proxy", func(t *testing.T) {
		proxy := proxyScenario(t, host, netproxy.ModeDirect, handler)
		_, err := CreateOrUpdateWebDAVBackup(cfg, []byte("payload"))
		require.Error(t, err)
		assert.Empty(t, proxy.Requests())
	})
}
