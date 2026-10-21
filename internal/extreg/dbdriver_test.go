package extreg

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/connpool"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
)

// driverDescriptor is a driver an extension provides in these tests.
func driverDescriptor(id string, aliases ...string) dbdriver.Descriptor {
	return dbdriver.Descriptor{
		ID: dbdriver.DriverID(id), Name: id, Icon: "database", Order: 500, Aliases: aliases, DefaultPort: 9000,
		Form: []dbdriver.FormTabFields{{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldDatabase,
		}}},
		Capabilities: []dbdriver.Capability{},
	}
}

// driverOnlyManifest is the merged manifest of an extension that provides drivers
// and nothing else: no asset type, no policy type.
func driverOnlyManifest(name string, drivers ...dbdriver.Descriptor) *extension.Manifest {
	m := &extension.Manifest{Name: name, Version: "1.0.0", DatabaseDrivers: drivers}
	for _, d := range drivers {
		m.Capabilities.DatabaseDrivers = append(m.Capabilities.DatabaseDrivers, string(d.ID))
	}
	return m
}

func registerManifest(t *testing.T, m *extension.Manifest, plugin pluginCaller) error {
	t.Helper()
	err := register(loaded{name: m.Name, manifest: m, plugin: plugin, translate: (&extension.Extension{}).Translate}, "help", "desc")
	if err == nil {
		t.Cleanup(func() { Unregister(m.Name) })
	}
	return err
}

// fakeDriverPlugin is a plugin whose drivers open dbdrivertest sessions and records
// every OpenDriverSession call.
type fakeDriverPlugin struct {
	fakePlugin
	mu       sync.Mutex
	ids      []string
	requests []dbdriver.OpenRequest
	sessions []*gatedSession
}

func newFakeDriverPlugin() *fakeDriverPlugin {
	p := &fakeDriverPlugin{}
	p.openDriver = func(ctx context.Context, id string, req dbdriver.OpenRequest) (dbdriver.Session, error) {
		inner, err := dbdrivertest.NewFake().Open(ctx, req)
		if err != nil {
			return nil, err
		}
		s := &gatedSession{FakeSession: inner.(*dbdrivertest.FakeSession), started: make(chan struct{}, 8)}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.ids = append(p.ids, id)
		p.requests = append(p.requests, req)
		p.sessions = append(p.sessions, s)
		return s, nil
	}
	return p
}

func (p *fakeDriverPlugin) opened() ([]string, []dbdriver.OpenRequest, []*gatedSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.ids...), append([]dbdriver.OpenRequest(nil), p.requests...), append([]*gatedSession(nil), p.sessions...)
}

// gatedSession runs the statement "SLOW" until its ctx ends: a long query in
// flight, or one queued behind it.
type gatedSession struct {
	*dbdrivertest.FakeSession
	started chan struct{}
}

func (s *gatedSession) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	if statement == "SLOW" {
		s.started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.FakeSession.Execute(ctx, statement)
}

func TestDriverOnlyExtensionRegistersItsDrivers(t *testing.T) {
	require.NoError(t, registerManifest(t, driverOnlyManifest("acme-db", driverDescriptor("acmedb", "acme")), newFakeDriverPlugin()))
	// A second driver-only extension: neither declares a policy type, so they share none.
	require.NoError(t, registerManifest(t, driverOnlyManifest("beta-db", driverDescriptor("betadb")), newFakeDriverPlugin()))

	d, err := dbregistry.Lookup("acmedb")
	require.NoError(t, err)
	assert.Equal(t, dbdriver.DriverID("acmedb"), d.Descriptor().ID)
	byAlias, ok := dbregistry.ByName("acme")
	require.True(t, ok)
	assert.Equal(t, dbdriver.DriverID("acmedb"), byAlias.Descriptor().ID)
	_, err = dbregistry.Lookup("betadb")
	require.NoError(t, err)

	Unregister("acme-db")
	_, err = dbregistry.Lookup("acmedb")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	_, ok = dbregistry.ByName("acme")
	assert.False(t, ok)
	_, err = dbregistry.Lookup("betadb")
	assert.NoError(t, err, "unregistering one extension leaves another's driver alone")
}

// The labels and descriptions of a driver's own fields go through the extension's
// locales, in the language the caller asks for — as an extension asset type's
// config fields do.
func TestExtensionDriverFieldsAreLocalizedThroughItsLocales(t *testing.T) {
	desc := driverDescriptor("acmedb")
	desc.DriverFields = []dbdriver.DriverField{{
		Key: "sid", Type: dbdriver.FieldTypeText, Tab: dbdriver.TabConnection, Label: "field.sid", Description: "field.sid.help",
	}}
	m := driverOnlyManifest("acme-db", desc)
	require.NoError(t, Register(&extension.Extension{Name: m.Name, Manifest: m, Locales: map[string]map[string]string{
		"zh-cn": {"field.sid": "实例 SID", "field.sid.help": "数据库实例标识"},
		"en":    {"field.sid": "Instance SID"},
	}}))
	t.Cleanup(func() { Unregister(m.Name) })

	d, err := dbregistry.Lookup("acmedb")
	require.NoError(t, err)
	zh := dbregistry.LocalizedDescriptor(d, "zh-CN").DriverFields[0]
	assert.Equal(t, "实例 SID", zh.Label)
	assert.Equal(t, "数据库实例标识", zh.Description)
	en := dbregistry.LocalizedDescriptor(d, "en").DriverFields[0]
	assert.Equal(t, "Instance SID", en.Label)
	assert.Equal(t, "field.sid.help", en.Description, "a key without a translation stays as written")
	assert.Equal(t, "field.sid", d.Descriptor().DriverFields[0].Label, "the registered descriptor is not rewritten")
}

func TestDriverNameClashRefusesTheWholeExtension(t *testing.T) {
	t.Run("with a built-in driver", func(t *testing.T) {
		dbdrivertest.Register(t, dbdrivertest.NewFake()) // stands in for a built-in: fakedb, alias fake
		m := testManifest()
		m.DatabaseDrivers = []dbdriver.Descriptor{driverDescriptor("acmedb", "fake")}
		m.Capabilities.DatabaseDrivers = []string{"acmedb"}

		var err error
		require.NotPanics(t, func() { err = registerManifest(t, m, newFakeDriverPlugin()) })
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"acme"`, "names the refused extension")
		assert.Contains(t, err.Error(), `"fake"`, "names the clashing name")
		assert.Contains(t, err.Error(), `built-in driver "fakedb"`, "names the owner")

		_, ok := assettype.Get("acme-store")
		assert.False(t, ok, "the extension's asset type is not registered either")
		_, err = dbregistry.Lookup("acmedb")
		assert.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
		owner, err := dbregistry.Lookup("fakedb")
		require.NoError(t, err)
		assert.Equal(t, "FakeDB", owner.Descriptor().Name, "the owner is unaffected")

		// Nothing of the refused extension lingers: without the clash it registers.
		m.DatabaseDrivers = []dbdriver.Descriptor{driverDescriptor("acmedb")}
		require.NoError(t, registerManifest(t, m, newFakeDriverPlugin()))
	})

	t.Run("with another extension's driver", func(t *testing.T) {
		require.NoError(t, registerManifest(t, driverOnlyManifest("first", driverDescriptor("shared")), newFakeDriverPlugin()))
		second := driverOnlyManifest("second", driverDescriptor("own"), driverDescriptor("seconddb", "shared"))

		var err error
		require.NotPanics(t, func() { err = registerManifest(t, second, newFakeDriverPlugin()) })
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"second"`)
		assert.Contains(t, err.Error(), `"shared"`)
		assert.Contains(t, err.Error(), `extension "first"`)

		_, err = dbregistry.Lookup("own")
		assert.ErrorIs(t, err, dbregistry.ErrDriverUnavailable, "a driver registered before the clash is rolled back")
		_, err = dbregistry.Lookup("shared")
		assert.NoError(t, err, "the first extension keeps its driver")
	})
}

// The extension's driver is reached the way every driver is: connpool resolves the
// asset into the request, and the extension gets exactly that request.
func TestExtensionDriverOpensThroughThePluginWithTheHostBuiltRequest(t *testing.T) {
	credential_svc.SetDefault(credential_svc.New("driver-options-test-key", []byte("0123456789abcdef")))
	encryptedOption, err := credential_svc.Default().Encrypt("opt-s3cret")
	require.NoError(t, err)
	core, logs := observer.New(zap.DebugLevel)
	origLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(origLogger) })

	plugin := newFakeDriverPlugin()
	desc := driverDescriptor("acmedb")
	desc.DriverFields = []dbdriver.DriverField{
		{Key: "tenant", Type: dbdriver.FieldTypeText, Tab: dbdriver.TabAdvanced},
		{Key: "token", Type: dbdriver.FieldTypePassword, Tab: dbdriver.TabAdvanced},
	}
	require.NoError(t, registerManifest(t, driverOnlyManifest("acme-db", desc), plugin))

	cfg := &asset_entity.DatabaseConfig{
		Driver: "acmedb", Host: "10.0.0.5", Port: 9000, Username: "app", Database: "orders",
		Password: "ciphertext", CredentialID: 7,
		// The proxy does not exist: the request carries the dial, nothing dials it here.
		Proxy:         &asset_entity.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 1, Password: "proxy-secret"},
		DriverOptions: map[string]any{"tenant": "acme", "token": encryptedOption},
	}
	session, err := connpool.OpenDatabase(context.Background(), &asset_entity.Asset{ID: 3}, cfg, "s3cret", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	ids, requests, _ := plugin.opened()
	require.Equal(t, []string{"acmedb"}, ids)
	req := requests[0]
	assert.Equal(t, cfg.DriverConfig(), req.Config, "the driver's view of the config, without the host-owned parts")
	assert.Equal(t, "s3cret", req.Password, "the decrypted password")
	assert.NotNil(t, req.Transport.Dial, "the asset's reach path")
	assert.Equal(t, map[string]any{"tenant": "acme", "token": "opt-s3cret"}, req.DriverOptions,
		"the driver options, password-type ones decrypted")
	assert.Equal(t, encryptedOption, cfg.DriverOptions["token"], "the stored config keeps the ciphertext")
	for _, entry := range logs.All() {
		assert.NotContains(t, fmt.Sprint(entry.Message, entry.ContextMap()), "opt-s3cret")
	}

	res, err := session.Execute(context.Background(), "SELECT * FROM t")
	require.NoError(t, err)
	assert.Len(t, res.Rows.Rows, 3)
}

func TestUnregisteringAnExtensionClosesItsDriverSessions(t *testing.T) {
	plugin := newFakeDriverPlugin()
	m := driverOnlyManifest("acme-db", driverDescriptor("acmedb"))
	require.NoError(t, registerManifest(t, m, plugin))
	open := func() dbdriver.Session {
		t.Helper()
		d, err := dbregistry.Lookup("acmedb")
		require.NoError(t, err)
		s, err := d.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "acmedb", Host: "h"}})
		require.NoError(t, err)
		return s
	}
	busy, idle := open(), open()
	_, _, inner := plugin.opened()

	// Two operations in flight on the same session.
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := busy.Execute(context.Background(), "SLOW")
			errs <- err
		}()
		<-inner[0].started
	}

	Unregister("acme-db")

	for range 2 {
		select {
		case err := <-errs:
			require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
			assert.Contains(t, err.Error(), `"acmedb"`)
		case <-time.After(5 * time.Second):
			t.Fatal("an operation in flight when the driver went away did not end")
		}
	}
	for i, s := range inner {
		assert.True(t, s.Closed(), "session %d is closed", i)
	}
	_, err := idle.Execute(context.Background(), "SELECT 1")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), `"acmedb"`)
	assert.Len(t, inner[1].Statements(), 0, "a session of a removed driver runs nothing")
	assert.NoError(t, idle.Close(), "closing a session the removal already closed is fine")

	// Enabled again: the same driver id opens a session without anything else changing.
	require.NoError(t, registerManifest(t, m, plugin))
	res, err := open().Execute(context.Background(), "SELECT * FROM t")
	require.NoError(t, err)
	assert.Len(t, res.Rows.Rows, 3)
}
