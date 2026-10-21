package extension_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

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
// whose capabilities all count their use. network, when set, is the manifest's
// capabilities.network; declaring databaseRedirects raises its hostABI to the one
// that reads it.
func loadDriverFixture(t *testing.T, spy *hostSpy, network map[string]any) *extension.Extension {
	t.Helper()
	root := t.TempDir()
	dir := extensiontest.InstallDBDriverFixture(t, root)
	if network != nil {
		path := filepath.Join(dir, "manifest.json")
		raw, err := os.ReadFile(path) //nolint:gosec // this test's own temp dir
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		m["capabilities"].(map[string]any)["network"] = network
		if network["databaseRedirects"] == true {
			m["hostABI"] = extension.DatabaseExtrasMinHostABI
		}
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
		var network map[string]any
		if withEndpoint {
			name = "with network.assetEndpoint"
			network = map[string]any{"assetEndpoint": true}
		}
		t.Run(name, func(t *testing.T) {
			spy := &hostSpy{}
			ext := loadDriverFixture(t, spy, network)
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
					assert.Contains(t, msg, "does not declare following database redirects", "the refusal says why")
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

// redirectRoute is a reach path with two ends to tell apart: the asset's own dial
// (Transport.Dial) and the by-address dial (Transport.DialAddr).
type redirectRoute struct {
	asset, byAddr *extension.FixtureDBServer
}

func newRedirectRoute(t *testing.T) redirectRoute {
	return redirectRoute{asset: extension.NewFixtureDBServer(t), byAddr: extension.NewFixtureDBServer(t)}
}

// openRedirectSession opens a session of the fixture driver on host along
// transport, as the host does for asset 42.
func openRedirectSession(t *testing.T, ext *extension.Extension, host string, transport dbdriver.Transport) dbdriver.Session {
	t.Helper()
	desc := ext.Manifest.DatabaseDrivers[0]
	s, err := ext.Plugin.OpenDriverSession(context.Background(), string(desc.ID), dbdriver.OpenRequest{
		Config:    &dbdriver.Config{Driver: desc.ID, Host: host, Port: desc.DefaultPort, Username: "alice", Database: "app"},
		Password:  "s3cret",
		Transport: transport,
		AssetID:   42,
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	return s
}

// redirectLogs counts, by address, the Info entries that record a session
// following a redirect.
func redirectLogs(t *testing.T, logs *observer.ObservedLogs) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for _, e := range logs.All() {
		fields := e.ContextMap()
		assert.NotContains(t, fmt.Sprint(e.Message, fields), "s3cret")
		if e.Level != zap.InfoLevel || fields["addr"] == nil {
			continue
		}
		assert.Equal(t, "fixture-dbdriver", fields["extension"])
		assert.Equal(t, extensiontest.DBDriverFixtureDriver, fields["driver"])
		assert.EqualValues(t, 42, fields["assetID"])
		seen[fields["addr"].(string)]++
	}
	return seen
}

func TestDriverDialFollowsDatabaseRedirects(t *testing.T) {
	ext := loadDriverFixture(t, &hostSpy{}, map[string]any{"databaseRedirects": true})

	t.Run("through a tunnel or proxy any address is dialed by address along the route, unresolved", func(t *testing.T) {
		route := newRedirectRoute(t)
		s := openRedirectSession(t, ext, "db.internal", dbdriver.Transport{Dial: route.asset.Dial, DialAddr: route.byAddr.Dial})

		for _, addr := range []string{"node2.internal:7400", "127.0.0.1:7401", "localhost:7402"} {
			assert.Empty(t, probe(t, s, "DIAL "+addr), addr)
		}
		assert.Empty(t, probe(t, s, "DIAL db.internal:7357"))

		assert.Equal(t, []string{"node2.internal:7400", "127.0.0.1:7401", "localhost:7402"}, route.byAddr.Dialed(),
			"another address goes by address along the route; its name is the far end's to resolve")
		assert.Equal(t, []string{"db.internal:7357", "db.internal:7357"}, route.asset.Dialed(),
			"the asset's own address still takes the asset's dial: the login, then the probe")
	})

	t.Run("each address other than the asset's is logged once per session", func(t *testing.T) {
		logs := observeLogs(t)
		route := newRedirectRoute(t)
		s := openRedirectSession(t, ext, "db.internal", dbdriver.Transport{Dial: route.asset.Dial, DialAddr: route.byAddr.Dial})
		for _, addr := range []string{"node2.internal:7400", "node2.internal:7400", "node3.internal:7400", "db.internal:7357"} {
			assert.Empty(t, probe(t, s, "DIAL "+addr), addr)
		}
		assert.Equal(t, map[string]int{"node2.internal:7400": 1, "node3.internal:7400": 1}, redirectLogs(t, logs))

		other := openRedirectSession(t, ext, "db.internal", dbdriver.Transport{Dial: route.asset.Dial, DialAddr: route.byAddr.Dial})
		assert.Empty(t, probe(t, other, "DIAL node2.internal:7400"))
		assert.Equal(t, map[string]int{"node2.internal:7400": 2, "node3.internal:7400": 1}, redirectLogs(t, logs),
			"another session logs its own first dial")
	})

	t.Run("on a direct route, loopback and link-local addresses are refused by name", func(t *testing.T) {
		logs := observeLogs(t)
		route := newRedirectRoute(t)
		s := openRedirectSession(t, ext, "203.0.113.10", dbdriver.Transport{Dial: route.asset.Dial, DialAddr: route.byAddr.Dial, Direct: true})

		local := []string{"127.0.0.1:7400", "127.8.0.1:7400", "[::1]:7400", "[::ffff:127.0.0.1]:7400", "localhost:7400", "169.254.10.1:7400", "[fe80::1]:7400", "0.0.0.0:7400"}
		for _, addr := range local {
			assert.Contains(t, probe(t, s, "DIAL "+addr), addr, "the refused address is named")
		}
		assert.Empty(t, route.byAddr.Dialed(), "nothing refused is dialed")
		refused := 0
		for _, e := range logs.All() {
			if e.Level == zap.WarnLevel {
				refused++
			}
		}
		assert.Equal(t, len(local), refused, "each refusal is a Warn")
		assert.Empty(t, redirectLogs(t, logs), "a refused address is not logged as a redirect followed")

		assert.Empty(t, probe(t, s, "DIAL 203.0.113.20:7400"))
		assert.Equal(t, []string{"203.0.113.20:7400"}, route.byAddr.Dialed(), "any other address is dialed")
	})

	t.Run("a route without a by-address dial is direct: loopback redirects are refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		route := newRedirectRoute(t)
		s := openRedirectSession(t, ext, "203.0.113.10", dbdriver.Transport{Dial: route.asset.Dial})
		addr := ln.Addr().String()
		msg := probe(t, s, "DIAL "+addr)
		assert.Contains(t, msg, addr, "the refused address is named")
		assert.Contains(t, msg, "loopback", "a direct dial gets the direct route's check")
	})

	t.Run("on a direct route to a loopback asset, loopback redirects are allowed", func(t *testing.T) {
		route := newRedirectRoute(t)
		s := openRedirectSession(t, ext, "127.0.0.1", dbdriver.Transport{Dial: route.asset.Dial, DialAddr: route.byAddr.Dial, Direct: true})
		for _, addr := range []string{"127.0.0.1:7401", "localhost:7402"} {
			assert.Empty(t, probe(t, s, "DIAL "+addr), addr)
		}
		assert.Equal(t, []string{"127.0.0.1:7401", "localhost:7402"}, route.byAddr.Dialed())
	})

	t.Run("an address the route cannot reach fails, naming the address and the cause", func(t *testing.T) {
		route := newRedirectRoute(t)
		unreachable := func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("connect: connection refused")
		}
		s := openRedirectSession(t, ext, "db.internal", dbdriver.Transport{Dial: route.asset.Dial, DialAddr: unreachable})
		msg := probe(t, s, "DIAL node9.internal:7400")
		assert.Contains(t, msg, "node9.internal:7400")
		assert.Contains(t, msg, "connection refused")
	})
}
