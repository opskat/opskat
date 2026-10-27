package update_svc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"
)

// useNetworkProxy 把进程级外网代理设置换成 s，测试结束恢复。调用它的测试不能 t.Parallel。
func useNetworkProxy(t *testing.T, s netproxy.Settings) {
	t.Helper()
	prev := netproxy.Get()
	netproxy.Set(s)
	t.Cleanup(func() { netproxy.Set(prev) })
}

const (
	apiHost    = "api.update.test"
	assetsHost = "assets.update.test"
	mirrorHost = "mirror.update.test"
)

// fakeGitHub 起一个本地服务并把三个非回环主机名转发到它：release API、资产下载与镜像。
func fakeGitHub(t *testing.T, proxy *netproxytest.Proxy) {
	t.Helper()
	platform := runtime.GOOS + "-" + runtime.GOARCH
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/opskat/opskat/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[`+
			`{"name":"opskat-%s.zip","browser_download_url":"http://%s/opskat.zip","size":1},`+
			`{"name":"SHA256SUMS.txt","browser_download_url":"http://%s/SHA256SUMS.txt","size":1}]}`,
			platform, assetsHost, assetsHost)
	})
	mux.HandleFunc("/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {})
	// 资产下载故意失败，下载流程停在安装之前。
	mux.HandleFunc("/opskat.zip", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "stop before install", http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	for _, h := range []string{apiHost, assetsHost, mirrorHost} {
		proxy.Forward(h, addr)
	}
	prev := apiBaseURL
	apiBaseURL = "http://" + apiHost + "/repos/" + githubRepo
	t.Cleanup(func() { apiBaseURL = prev })
}

func proxiedTargets(proxy *netproxytest.Proxy) []string {
	reqs := proxy.Requests()
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Target)
	}
	return out
}

func TestUpdateRequestsFollowNetworkProxy(t *testing.T) {
	steps := map[string]struct {
		hosts []string
		run   func() error
	}{
		"check for update": {[]string{apiHost}, func() error {
			_, err := CheckForUpdate(ChannelStable, "")
			return err
		}},
		"mirror fallback": {[]string{mirrorHost}, func() error {
			_, err := fetchReleaseFromMirror(ChannelStable, "http://"+mirrorHost+"/")
			return err
		}},
		"checksums": {[]string{assetsHost}, func() error {
			_, err := FetchChecksums([]ReleaseAsset{{Name: "SHA256SUMS.txt", BrowserDownloadURL: "http://" + assetsHost + "/SHA256SUMS.txt"}})
			return err
		}},
		"download": {[]string{apiHost, assetsHost}, func() error {
			err := DownloadAndUpdate(ChannelStable, "", false, nil)
			if err == nil || !strings.Contains(err.Error(), "status 404") {
				return fmt.Errorf("download did not reach the asset server: %v", err)
			}
			return nil
		}},
	}
	for name, step := range steps {
		t.Run(name+" via custom proxy", func(t *testing.T) {
			proxy := netproxytest.NewRecordingProxy(t)
			fakeGitHub(t, proxy)
			useNetworkProxy(t, proxy.Settings(netproxy.SchemeHTTP))

			require.NoError(t, step.run())
			for _, h := range step.hosts {
				assert.Contains(t, proxiedTargets(proxy), h+":80")
			}
		})
		t.Run(name+" direct", func(t *testing.T) {
			proxy := netproxytest.NewRecordingProxy(t)
			fakeGitHub(t, proxy)
			s := proxy.Settings(netproxy.SchemeHTTP)
			s.Mode = netproxy.ModeDirect
			useNetworkProxy(t, s)

			_ = step.run() // 主机名只有代理能解析，直连必然失败；这里只关心代理没被用到
			assert.Empty(t, proxy.Requests())
		})
	}
}
