// Extension-store mock for the e2e harness: the official index and the OCI
// registry the store installs from, in one plain-http server. Started as a
// Playwright `webServer` (see playwright.config.ts) and reached by the real app
// through the OPSKAT_E2E_EXT_INDEX_URL / _KEYS / _REGISTRY overrides
// (internal/service/extstore_svc/endpoints.go), which only a run marked
// OPSKAT_E2E=1 honors.
//
// At startup it builds fixtures/store-ext for wasip1, packs it the way the
// extensions repo's publisher does (manifest.json + main.wasm + locales in a zip,
// capabilities copied from the manifest into the index), signs index.json with
// the ed25519 seed it is given, and only then listens — so "port open" means the
// store is ready.
//
// The index lists two extensions served from the same package:
//   - store-demo: the real sha256 — installs.
//   - store-tampered: a wrong sha256 — the download fails verification, so the
//     store must refuse it and name the expected and actual digests.
//
// The registry asks for an anonymous Bearer token first (as ghcr.io does) and
// streams the blob in slices with pauses, so the download progress is visible.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

const (
	guestDir  = "e2e/fixtures/store-ext"
	repoBase  = "opskat/extensions/"
	blobParts = 8
	partPause = 250 * time.Millisecond
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ext-store-mock <port> <base64 ed25519 seed>")
		os.Exit(1)
	}
	port, err := strconv.Atoi(os.Args[1])
	if err != nil || port <= 0 {
		fail("invalid port %q", os.Args[1])
	}
	seed, err := base64.StdEncoding.DecodeString(os.Args[2])
	if err != nil || len(seed) != ed25519.SeedSize {
		fail("seed must be base64 of %d bytes", ed25519.SeedSize)
	}

	manifestJSON, err := os.ReadFile(filepath.Join(guestDir, "manifest.json"))
	if err != nil {
		fail("read manifest: %v", err)
	}
	var m extension.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		fail("parse manifest: %v", err)
	}
	pkg := buildPackage(manifestJSON)
	sum := sha256.Sum256(pkg)
	digest := hex.EncodeToString(sum[:])

	index := extstore.Index{Format: extstore.FormatVersion}
	for _, e := range []struct{ name, sha, display string }{
		{m.Name, digest, "Store Demo"},
		{"store-tampered", strings.Repeat("0", 64), "Store Tampered"},
	} {
		index.Extensions = append(index.Extensions, extstore.Extension{
			Name:    e.name,
			Icon:    "package",
			Display: map[string]extstore.Display{"en": {Name: e.display, Description: "e2e store fixture"}},
			Versions: []extstore.Version{{
				Version:      m.Version,
				HostABI:      m.HostABI,
				Capabilities: m.Capabilities,
				Size:         int64(len(pkg)),
				PublishedAt:  time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
				Source: extstore.Source{
					Type:   extstore.SourceOCI,
					Ref:    "ghcr.io/" + repoBase + e.name + ":" + m.Version,
					SHA256: e.sha,
				},
			}},
		})
	}
	raw, err := json.Marshal(index)
	if err != nil {
		fail("marshal index: %v", err)
	}
	sig := extstore.Sign(raw, ed25519.NewKeyFromSeed(seed))

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(raw) })
	mux.HandleFunc("/index.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(sig) //nolint:gosec // a signature this fixture computed, not request data
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "anonymous"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer anonymous" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="http://%s/token",service="e2e"`, r.Host))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.oci.image.manifest.v1+json",
				"artifactType":  "application/vnd.opskat.extension.v1",
				"config": map[string]any{
					"mediaType": "application/vnd.oci.empty.v1+json",
					"digest":    "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
					"size":      2,
				},
				"layers": []map[string]any{{
					"mediaType": "application/zip", "digest": "sha256:" + digest, "size": len(pkg),
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/blobs/sha256:"+digest):
			serveSlowly(w, pkg)
		default:
			http.NotFound(w, r)
		}
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("ext-store-mock: %s (%s %s, %d bytes)\n", addr, m.Name, m.Version, len(pkg))
	if err := http.ListenAndServe(addr, mux); err != nil { //nolint:gosec // test fixture, no timeouts needed
		fail("listen: %v", err)
	}
}

// buildPackage compiles the guest and zips it with its manifest and locales.
func buildPackage(manifestJSON []byte) []byte {
	tmp, err := os.MkdirTemp("", "ext-store-mock-*")
	if err != nil {
		fail("temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	wasm := filepath.Join(tmp, "main.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasm, "./"+guestDir) //nolint:gosec // fixed argv
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		fail("build guest: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write(data)
		}
		if err != nil {
			fail("zip %s: %v", name, err)
		}
	}
	add("manifest.json", manifestJSON)
	add("main.wasm", readFile(wasm))
	locales, err := filepath.Glob(filepath.Join(guestDir, "locales", "*.json"))
	if err != nil {
		fail("locales: %v", err)
	}
	for _, l := range locales {
		add("locales/"+filepath.Base(l), readFile(l))
	}
	if err := zw.Close(); err != nil {
		fail("zip: %v", err)
	}
	return buf.Bytes()
}

// serveSlowly writes data in blobParts slices with a pause between them.
func serveSlowly(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	flusher, _ := w.(http.Flusher)
	part := (len(data) + blobParts - 1) / blobParts
	for off := 0; off < len(data); off += part {
		end := min(off+part, len(data))
		if _, err := w.Write(data[off:end]); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		if end < len(data) {
			time.Sleep(partPause)
		}
	}
}

func readFile(path string) []byte {
	data, err := os.ReadFile(path) //nolint:gosec // fixture paths
	if err != nil {
		fail("read %s: %v", path, err)
	}
	return data
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ext-store-mock: "+format+"\n", args...)
	os.Exit(1)
}
