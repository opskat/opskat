package ociclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRef = "opskat/extensions/demo:1.2.0"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// mockRegistry 是最小的 OCI registry：可选 Bearer 质询、单层清单、blob。
type mockRegistry struct {
	srv         *httptest.Server
	blob        []byte
	auth        bool // 要求 Bearer 令牌
	tokenStatus int  // 令牌端点状态码，0 = 200
	manifest    func() (int, string, any)
	mu          sync.Mutex
	tokenCalls  int
	scopes      []string
	blobAuth    string
}

func newMock(t *testing.T, blob []byte, auth bool) *mockRegistry {
	t.Helper()
	m := &mockRegistry{blob: blob, auth: auth}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

// registry 是 m 的明文 registry。
func (m *mockRegistry) registry() Registry { return plain(m.srv.URL) }

// plain 把 httptest 的 URL 变成明文 Registry。
func plain(u string) Registry {
	return Registry{Host: strings.TrimPrefix(u, "http://"), PlainHTTP: true}
}

func (m *mockRegistry) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		m.mu.Lock()
		m.tokenCalls++
		m.scopes = append(m.scopes, r.URL.Query().Get("scope")+"|"+r.URL.Query().Get("service"))
		m.mu.Unlock()
		if m.tokenStatus != 0 {
			w.WriteHeader(m.tokenStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		return
	}
	if m.auth && r.Header.Get("Authorization") != "Bearer tok" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(
			`Bearer realm="%s/token",service="reg.test",scope="repository:opskat/extensions/demo:pull"`, m.srv.URL))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/manifests/1.2.0"):
		if m.manifest != nil {
			code, ct, body := m.manifest()
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"layers": []map[string]any{{
				"mediaType": "application/zip", "digest": "sha256:" + sum(m.blob), "size": len(m.blob),
			}},
		})
	case strings.Contains(r.URL.Path, "/blobs/sha256:"):
		m.mu.Lock()
		m.blobAuth = r.Header.Get("Authorization")
		m.mu.Unlock()
		_, _ = w.Write(m.blob)
	default:
		http.NotFound(w, r)
	}
}

func dstPath(t *testing.T) string { return filepath.Join(t.TempDir(), "demo.zip") }

func TestPullWithAuthChallenge(t *testing.T) {
	blob := []byte(strings.Repeat("zipdata", 1000))
	m := newMock(t, blob, true)
	dst := dstPath(t)
	var lastDone, lastTotal int64
	err := Pull(context.Background(), m.registry(), testRef, sum(blob), 1<<20, dst, func(d, total int64) {
		lastDone, lastTotal = d, total
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst) //nolint:gosec // test temp path
	if string(got) != string(blob) {
		t.Fatal("content mismatch")
	}
	if m.tokenCalls != 1 || m.scopes[0] != "repository:opskat/extensions/demo:pull|reg.test" {
		t.Fatalf("token calls = %d scopes = %v", m.tokenCalls, m.scopes)
	}
	if m.blobAuth != "Bearer tok" {
		t.Fatalf("blob auth = %q", m.blobAuth)
	}
	if lastDone != int64(len(blob)) || lastTotal != int64(len(blob)) {
		t.Fatalf("progress = %d/%d", lastDone, lastTotal)
	}
}

func TestPullNoAuthRegistryAndCustomHostPort(t *testing.T) {
	blob := []byte("hello")
	m := newMock(t, blob, false)
	if !strings.Contains(m.registry().Host, ":") {
		t.Fatal("expected host:port")
	}
	dst := dstPath(t)
	if err := Pull(context.Background(), m.registry(), testRef, sum(blob), 1024, dst, nil); err != nil {
		t.Fatal(err)
	}
	if m.tokenCalls != 0 {
		t.Fatal("token requested without challenge")
	}
}

func TestPullOversizeAborts(t *testing.T) {
	blob := []byte(strings.Repeat("x", 5000))
	m := newMock(t, blob, false)
	dst := dstPath(t)
	err := Pull(context.Background(), m.registry(), testRef, sum(blob), 1000, dst, nil)
	if !errors.Is(err, ErrSize) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("file should not exist")
	}
}

// 清单谎报小尺寸、实际 blob 超大：下载中途也必须中止。
func TestPullOversizeStreamAborts(t *testing.T) {
	blob := []byte(strings.Repeat("x", 5000))
	m := newMock(t, blob, false)
	m.manifest = func() (int, string, any) {
		return 200, "application/vnd.oci.image.manifest.v1+json", map[string]any{
			"layers": []map[string]any{{"digest": "sha256:" + sum(blob), "size": 10}},
		}
	}
	dst := dstPath(t)
	err := Pull(context.Background(), m.registry(), testRef, sum(blob), 1000, dst, nil)
	if !errors.Is(err, ErrSize) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("file should be removed")
	}
}

func TestPullDigestMismatchDeletesFile(t *testing.T) {
	blob := []byte("real content")
	m := newMock(t, blob, false)
	dst := dstPath(t)
	want := sum([]byte("other"))
	err := Pull(context.Background(), m.registry(), testRef, want, 1024, dst, nil)
	if !errors.Is(err, ErrDigest) {
		t.Fatalf("err = %v", err)
	}
	var de *DigestError
	if !errors.As(err, &de) || de.Expected != want || de.Actual != sum(blob) {
		t.Fatalf("digest error = %+v", de)
	}
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), sum(blob)) {
		t.Fatalf("message lacks values: %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("file should be removed")
	}
}

func TestPullAuthFailures(t *testing.T) {
	blob := []byte("x")
	m := newMock(t, blob, true)
	m.tokenStatus = http.StatusForbidden
	err := Pull(context.Background(), m.registry(), testRef, sum(blob), 10, dstPath(t), nil)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("token denied: %v", err)
	}

	// 无 Bearer 质询的 401
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	err = Pull(context.Background(), plain(srv.URL), testRef, sum(blob), 10, dstPath(t), nil)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("no challenge: %v", err)
	}
}

func TestPullNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	reg := plain(srv.URL)
	srv.Close()
	err := Pull(context.Background(), reg, testRef, sum(nil), 10, dstPath(t), nil)
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, ErrAuth) || errors.Is(err, ErrSize) || errors.Is(err, ErrDigest) {
		t.Fatal("kinds must be distinct")
	}
}

func TestPullManifestErrors(t *testing.T) {
	blob := []byte("x")
	m := newMock(t, blob, false)
	cases := map[string]func() (int, string, any){
		"not found": func() (int, string, any) { return 404, "application/json", map[string]string{} },
		"two layers": func() (int, string, any) {
			return 200, "application/vnd.oci.image.manifest.v1+json", map[string]any{
				"layers": []map[string]any{{"digest": "sha256:a", "size": 1}, {"digest": "sha256:b", "size": 1}},
			}
		},
		"index manifest": func() (int, string, any) {
			return 200, "application/vnd.oci.image.index.v1+json", map[string]any{"manifests": []any{}}
		},
	}
	for name, fn := range cases {
		m.manifest = fn
		err := Pull(context.Background(), m.registry(), testRef, sum(blob), 10, dstPath(t), nil)
		if !errors.Is(err, ErrRegistry) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// 镜像站把上游 registry 放在路径下（<镜像>/ghcr.io/<仓库>）时，前缀是仓库名的
// 一部分：清单与 blob 都从 /v2/<前缀>/<仓库> 取，和 docker pull 的写法一致。
func TestPullPrefixedRegistryRequestsThePrefixedRepository(t *testing.T) {
	blob := []byte("mirrored-package")
	var mu sync.Mutex
	var paths []string
	m := &mockRegistry{blob: blob}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/v2/ghcr.io/opskat/extensions/demo/") {
			http.NotFound(w, r)
			return
		}
		m.serve(w, r)
	}))
	t.Cleanup(m.srv.Close)

	reg := m.registry()
	reg.Prefix = "ghcr.io"
	dst := dstPath(t)
	if err := Pull(context.Background(), reg, testRef, sum(blob), 1<<20, dst, nil); err != nil {
		t.Fatalf("Pull: %v (requested %v)", err, paths)
	}
	got, _ := os.ReadFile(dst) //nolint:gosec // test temp path
	if string(got) != string(blob) {
		t.Fatal("content mismatch")
	}
	want := []string{
		"/v2/ghcr.io/opskat/extensions/demo/manifests/1.2.0",
		"/v2/ghcr.io/opskat/extensions/demo/blobs/sha256:" + sum(blob),
	}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("requested %v, want %v", paths, want)
	}
}

func TestPullRejectsBadInput(t *testing.T) {
	for _, host := range []string{"", "https://ghcr.io", "http://ghcr.io", "ghcr.io/x", "a b"} {
		if err := Pull(context.Background(), Registry{Host: host}, testRef, sum(nil), 10, dstPath(t), nil); err == nil {
			t.Errorf("host %q accepted", host)
		}
	}
	for _, prefix := range []string{"/ghcr.io", "ghcr.io/", "a b", "a?b", "a#b", "a@b", `a\b`} {
		if err := Pull(context.Background(), Registry{Host: "h:1", Prefix: prefix}, testRef, sum(nil), 10, dstPath(t), nil); err == nil {
			t.Errorf("prefix %q accepted", prefix)
		}
	}
	for _, ref := range []string{"", "noversion", "a/b:", ":1"} {
		if err := Pull(context.Background(), Registry{Host: "h:1"}, ref, sum(nil), 10, dstPath(t), nil); err == nil {
			t.Errorf("ref %q accepted", ref)
		}
	}
}

// newPlainMock 是要求 Bearer 质询的明文 registry。
func newPlainMock(t *testing.T, blob []byte) *mockRegistry {
	t.Helper()
	m := &mockRegistry{blob: blob, auth: true}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func TestPullPlainHTTPRegistry(t *testing.T) {
	blob := []byte("plain-http-package")
	m := newPlainMock(t, blob)
	dst := dstPath(t)
	if err := Pull(context.Background(), m.registry(), testRef, sum(blob), 1<<20, dst, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst) //nolint:gosec // test temp path
	if string(got) != string(blob) {
		t.Fatal("content mismatch")
	}
	if m.tokenCalls != 1 {
		t.Fatalf("token calls = %d, want the challenge followed over http", m.tokenCalls)
	}
}

func TestPullDefaultsToHTTPS(t *testing.T) {
	blob := []byte("plain-http-package")
	m := newPlainMock(t, blob)
	reg := m.registry()
	reg.PlainHTTP = false
	err := Pull(context.Background(), reg, testRef, sum(blob), 1<<20, dstPath(t), nil)
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("err = %v, want ErrNetwork from a TLS handshake against a plain-http server", err)
	}
	if m.tokenCalls != 0 {
		t.Fatalf("token calls = %d, want no plain-http request", m.tokenCalls)
	}
}

// stallingRegistry answers like a registry until the step named by stallAt, where it
// stops sending (headers never come, or the body stops midway) and holds the
// connection open.
func stallingRegistry(t *testing.T, blob []byte, stallAt string) Registry {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/1.2.0"):
			if stallAt == "manifest" {
				<-release
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"layers": []map[string]any{{"digest": "sha256:" + sum(blob), "size": len(blob)}},
			})
		case strings.Contains(r.URL.Path, "/blobs/"):
			w.Header().Set("Content-Length", fmt.Sprint(len(blob)))
			_, _ = w.Write(blob[:len(blob)/2])
			w.(http.Flusher).Flush()
			<-release
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return plain(srv.URL)
}

// A registry that stops answering must not hold an install forever: the desktop
// store install has no other way to end it.
func TestPullFailsWhenTheRegistryStalls(t *testing.T) {
	old := stallTimeout
	stallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stallTimeout = old })

	blob := []byte("a package that never finishes downloading")
	for _, at := range []string{"manifest", "blob"} {
		t.Run("stalls at the "+at, func(t *testing.T) {
			reg := stallingRegistry(t, blob, at)
			dst := dstPath(t)
			done := make(chan error, 1)
			go func() { done <- Pull(context.Background(), reg, testRef, sum(blob), 1<<20, dst, nil) }()
			select {
			case err := <-done:
				if !errors.Is(err, ErrNetwork) {
					t.Fatalf("err = %v, want ErrNetwork", err)
				}
				if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("partial download left behind: %v", statErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Pull is still waiting on a registry that stopped sending")
			}
		})
	}
}

// The stall guard must not cut a pull that keeps receiving bytes, however long
// it takes as a whole, nor turn a caller's cancel into a network failure.
func TestPullStallGuardOnlyCountsSilence(t *testing.T) {
	old := stallTimeout
	stallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { stallTimeout = old })

	blob := []byte("0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/1.2.0"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"layers": []map[string]any{{"digest": "sha256:" + sum(blob), "size": len(blob)}},
			})
		case strings.Contains(r.URL.Path, "/blobs/"):
			w.Header().Set("Content-Length", fmt.Sprint(len(blob)))
			for i := range blob { // 10 × 100ms: longer than the guard, never silent for it
				_, _ = w.Write(blob[i : i+1])
				w.(http.Flusher).Flush()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}))
	t.Cleanup(srv.Close)
	if err := Pull(context.Background(), plain(srv.URL), testRef, sum(blob), 1<<20, dstPath(t), nil); err != nil {
		t.Fatalf("a slow but steady download failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Pull(ctx, plain(srv.URL), testRef, sum(blob), 1<<20, dstPath(t), nil)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrNetwork) {
		t.Fatalf("err = %v, want the caller's cancel", err)
	}
}
