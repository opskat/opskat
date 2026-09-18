package k8s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotPodLogsFetchesEntireCurrentLogFile(t *testing.T) {
	const body = "line-1\nline-2\nline-3\n"
	var gotPath, gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	reader, err := SnapshotPodLogs(context.Background(), testKubeconfig(srv.URL), "default", "api", "app")
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })

	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, string(got))
	require.Equal(t, "/api/v1/namespaces/default/pods/api/log", gotPath)
	require.Contains(t, gotRawQuery, "container=app")
	require.NotContains(t, gotRawQuery, "follow=true")
	require.NotContains(t, gotRawQuery, "tailLines=")
}

func TestLogDownloadFilename(t *testing.T) {
	require.Equal(t, "default-api-server-app.log", LogDownloadFilename("default", "api-server", "app"))
	require.Equal(t, "kube-system-coredns_abc.log", LogDownloadFilename("kube-system", "coredns/abc", ""))
}

func testKubeconfig(server string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user: {}
`, strings.TrimSpace(server))
}
