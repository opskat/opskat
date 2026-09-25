package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// The fixture extension in testdata/fixture-ext is the only way to observe the
// guest ABI end to end: everything below the WASM boundary — the reactor entry
// point, guest memory management, host call framing — has no meaning without a
// real module on the other side.

var (
	fixtureOnce sync.Once
	fixtureWASM []byte
	fixtureErr  error
)

// fixtureWasm compiles testdata/fixture-ext as a WASI reactor, once per test binary.
func fixtureWasm(t *testing.T) []byte {
	t.Helper()
	fixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "opskat-fixture-ext-*")
		if err != nil {
			fixtureErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		out := filepath.Join(dir, "main.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./testdata/fixture-ext") //nolint:gosec // fixed argv, only the temp output path varies
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if output, err := cmd.CombinedOutput(); err != nil {
			fixtureErr = fmt.Errorf("build fixture extension: %w\n%s", err, output)
			return
		}
		fixtureWASM, fixtureErr = os.ReadFile(out) //nolint:gosec // path built above
	})
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixtureWASM
}

// fixtureManifest parses the fixture's real manifest so capability tests run
// against the same declarations a shipped extension would use.
func fixtureManifest(t *testing.T) *Manifest {
	t.Helper()
	data, err := os.ReadFile("testdata/fixture-ext/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// recordedHost is a HostProvider that keeps everything the guest did to it, and
// wraps each opened resource so a test can tell whether the runtime closed it.
type recordedHost struct {
	mu        sync.Mutex
	logs      []string
	kv        map[string][]byte
	configs   map[int64]json.RawMessage
	events    []recordedEvent
	resources []*trackedCloser

	// When arrivals is set, KVSet announces itself and then waits for release.
	// A test that collects N arrivals before closing release has proven N calls
	// were inside the guest at the same time.
	arrivals chan struct{}
	release  chan struct{}
}

type recordedEvent struct {
	Invocation string
	Type       string
	Data       json.RawMessage
}

type trackedCloser struct {
	io.Closer
	closed int
}

func (c *trackedCloser) Close() error {
	c.closed++
	return c.Closer.Close()
}

func newRecordedHost() *recordedHost {
	return &recordedHost{
		kv:      map[string][]byte{},
		configs: map[int64]json.RawMessage{},
	}
}

// errWriteFailed is what a handle opened on a path ending in .fail reports on
// write, so a test can drive a mid-stream failure the guest has to notice.
var errWriteFailed = errors.New("simulated write failure")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWriteFailed }
func (failingWriter) Close() error              { return nil }

func (h *recordedHost) OpenIO(_ context.Context, _ *AssetRef, params IOOpenParams) (*IOResource, error) {
	if params.Type != "file" {
		return nil, fmt.Errorf("recordedHost only opens files, got %q", params.Type)
	}
	if strings.HasSuffix(params.Path, ".fail") {
		return &IOResource{Writer: failingWriter{}, Closer: failingWriter{}}, nil
	}
	res, err := OpenFileResource(params.Path, params.Mode)
	if err != nil {
		return nil, err
	}
	tracked := &trackedCloser{Closer: res.Closer}
	res.Closer = tracked
	h.mu.Lock()
	h.resources = append(h.resources, tracked)
	h.mu.Unlock()
	return res, nil
}

func (h *recordedHost) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cfg, ok := h.configs[assetID]
	if !ok {
		return nil, fmt.Errorf("no config for asset %d", assetID)
	}
	return cfg, nil
}

func (h *recordedHost) FileDialog(string, DialogOptions) (string, error) {
	return "", fmt.Errorf("no file dialog in tests")
}

func (h *recordedHost) Log(level, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, level+":"+msg)
}

func (h *recordedHost) KVGet(key string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.kv[key], nil
}

func (h *recordedHost) KVSet(key string, value []byte) error {
	if h.arrivals != nil {
		h.arrivals <- struct{}{}
		<-h.release
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kv[key] = value
	return nil
}

func (h *recordedHost) ActionEvent(invocationID, eventType string, data json.RawMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, recordedEvent{Invocation: invocationID, Type: eventType, Data: data})
	return nil
}

// eventsFor returns the events one invocation produced, in order.
func (h *recordedHost) eventsFor(invocationID string) []recordedEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []recordedEvent
	for _, e := range h.events {
		if e.Invocation == invocationID {
			out = append(out, e)
		}
	}
	return out
}

func (h *recordedHost) closedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.resources {
		n += r.closed
	}
	return n
}

var _ HostProvider = (*recordedHost)(nil)

func TestFixtureTypedConfigCredential(t *testing.T) {
	Convey("Given the fixture extension, whose config struct has a password field", t, func() {
		host := newRecordedHost()
		p := newFixturePlugin(t, host, t.TempDir())
		asset := &AssetRef{ID: 7, Name: "es", Type: "fixture"}

		Convey("without credentials:read the handle the desktop serves decodes, carrying no plaintext", func() {
			// What internal/app/extension serves an extension that does not
			// declare credentials:read: the stored password as an opaque handle.
			host.configs[7] = json.RawMessage(`{"endpoint":"https://es:9200","password":{"__credential_handle":"cred_0011223344556677"}}`)
			out := callToolOn(t, p, asset, "typed_config", map[string]any{})
			So(out["passwordSet"], ShouldEqual, true)
			So(out["password"], ShouldEqual, "")
			So(out["withheld"], ShouldEqual, true)
		})

		Convey("with credentials:read the plaintext the desktop serves is readable", func() {
			host.configs[7] = json.RawMessage(`{"endpoint":"https://es:9200","password":"s3cr3t"}`)
			out := callToolOn(t, p, asset, "typed_config", map[string]any{})
			So(out["passwordSet"], ShouldEqual, true)
			So(out["password"], ShouldEqual, "s3cr3t")
			So(out["withheld"], ShouldEqual, false)
		})

		Convey("an unset password decodes as unset", func() {
			host.configs[7] = json.RawMessage(`{"endpoint":"https://es:9200"}`)
			out := callToolOn(t, p, asset, "typed_config", map[string]any{})
			So(out["passwordSet"], ShouldEqual, false)
			So(out["password"], ShouldEqual, "")
			So(out["withheld"], ShouldEqual, false)
		})

		Convey("describe still reports the field as a secret the host encrypts and gates", func() {
			payload, err := p.Describe(context.Background())
			So(err, ShouldBeNil)
			desc, err := ParseDescriptor(payload)
			So(err, ShouldBeNil)
			So(desc.AssetTypes, ShouldHaveLength, 1)
			So(PasswordFieldsFromSchema(desc.AssetTypes[0].ConfigSchema), ShouldResemble, []string{"password"})
		})
	})
}
