package extension_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// hostSpy is every host capability a driver call must not reach, counting the
// times it is reached anyway.
type hostSpy struct{ kv, dialogs, configs atomic.Int32 }

func (h *hostSpy) Get(string) ([]byte, error) { h.kv.Add(1); return []byte("v"), nil }
func (h *hostSpy) Set(string, []byte) error   { h.kv.Add(1); return nil }
func (h *hostSpy) FileDialog(string, extension.DialogOptions) (string, error) {
	h.dialogs.Add(1)
	return "/etc/passwd", nil
}
func (h *hostSpy) GetAssetConfig(int64) (json.RawMessage, error) {
	h.configs.Add(1)
	return json.RawMessage(`{"password":"hunter2"}`), nil
}
func (h *hostSpy) AssetCredentialValues(context.Context, int64, []string) (map[string]string, error) {
	return nil, nil
}

// loadDriverFixture loads the fixture driver extension with a real host provider
// whose capabilities all count their use. withAssetEndpoint declares
// network.assetEndpoint in its manifest.
func loadDriverFixture(t *testing.T, spy *hostSpy, withAssetEndpoint bool) *extension.Extension {
	t.Helper()
	root := t.TempDir()
	dir := extensiontest.InstallDBDriverFixture(t, root)
	if withAssetEndpoint {
		path := filepath.Join(dir, "manifest.json")
		raw, err := os.ReadFile(path) //nolint:gosec // this test's own temp dir
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		m["capabilities"].(map[string]any)["network"] = map[string]any{"assetEndpoint": true}
		raw, err = json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
	}
	mgr := extension.NewManager(root, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{
			Logger: zap.NewNop(), KV: spy, FileDialogs: spy, AssetConfigs: spy,
		})
	}, zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })
	m, err := mgr.LoadExtension(context.Background(), dir)
	require.NoError(t, err)
	return mgr.GetExtension(m.Name)
}

// probe runs the fixture's host probe inside a driver call and answers the error
// the guest got from the host.
func probe(t *testing.T, s dbdriver.Session, what string) string {
	t.Helper()
	res, err := s.Execute(context.Background(), "SHOW HOST "+what)
	require.NoError(t, err)
	return res.Rows.Rows[0][0].(string)
}

func TestDriverCallHostCapabilities(t *testing.T) {
	for _, withEndpoint := range []bool{false, true} {
		name := "without network.assetEndpoint"
		if withEndpoint {
			name = "with network.assetEndpoint"
		}
		t.Run(name, func(t *testing.T) {
			spy := &hostSpy{}
			ext := loadDriverFixture(t, spy, withEndpoint)
			desc := ext.Manifest.DatabaseDrivers[0]
			d := &extensionDriver{
				NetworkRules: dbdriver.NetworkRules{DefaultPort: desc.DefaultPort},
				desc:         desc, plugin: ext.Plugin, server: extension.NewFixtureDBServer(t),
			}
			s := d.open(t)
			defer func() { assert.NoError(t, s.Close()) }()

			var httpHits atomic.Int32
			web := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { httpHits.Add(1) }))
			defer web.Close()

			t.Run("KV, file dialogs, HTTP and other asset config are unavailable", func(t *testing.T) {
				for what, target := range map[string]string{
					"KV": "", "KVSET": "", "FILE_DIALOG": "", "ASSET_CONFIG": "", "HTTP": " " + web.URL,
				} {
					msg := probe(t, s, what+target)
					assert.Contains(t, msg, "unavailable during driver calls", what)
				}
				assert.Zero(t, spy.kv.Load(), "the KV store is never reached")
				assert.Zero(t, spy.dialogs.Load(), "no file dialog opens")
				assert.Zero(t, spy.configs.Load(), "no asset config is read")
				assert.Zero(t, httpHits.Load(), "no HTTP request leaves the host")
			})

			t.Run("logging is still allowed", func(t *testing.T) {
				assert.Empty(t, probe(t, s, "LOG"))
			})

			t.Run("a dial to the asset's host:port goes through the reach path", func(t *testing.T) {
				before := len(d.server.Dialed())
				assert.Empty(t, probe(t, s, "DIAL db.internal:7357"))
				dialed := d.server.Dialed()
				require.Len(t, dialed, before+1)
				assert.Equal(t, "db.internal:7357", dialed[before])
			})

			t.Run("a dial to any other address is refused, named and logged", func(t *testing.T) {
				logs := observeLogs(t)
				before := len(d.server.Dialed())
				for _, addr := range []string{"other.internal:7357", "db.internal:7358"} {
					msg := probe(t, s, "DIAL "+addr)
					assert.Contains(t, msg, addr, "the refused address is named")
				}
				assert.Len(t, d.server.Dialed(), before, "the reach path never sees them")

				refused := 0
				for _, e := range logs.All() {
					assert.NotContains(t, e.Message, "s3cret")
					for _, f := range e.Context {
						assert.NotContains(t, f.String, "s3cret")
					}
					if e.Level != zap.WarnLevel {
						continue
					}
					for _, f := range e.Context {
						if f.String == "other.internal:7357" || f.String == "db.internal:7358" {
							refused++
						}
					}
				}
				assert.Equal(t, 2, refused, "each refusal is a Warn naming the address")
			})
		})
	}
}
