package extreg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// scriptCapabilities are the capabilities host ABI 1.1 added for extension drivers,
// each declared with what it needs (a dump writes the definitions TableDDL gives).
var scriptCapabilities = map[dbdriver.Capability][]dbdriver.Capability{
	dbdriver.CapRunSQLFile: {dbdriver.CapRunSQLFile},
	dbdriver.CapDumpSQL:    {dbdriver.CapViewDDL, dbdriver.CapDumpSQL},
}

// An extension built for hostABI 1.0 has no wire operations for running or dumping
// SQL files: one whose driver declares either is refused at load, naming the
// extension and the capability, and its drivers stay out of the registry.
func TestHostABI10DriverDeclaringAScriptCapabilityIsRefused(t *testing.T) {
	for capability, declared := range scriptCapabilities {
		t.Run(string(capability), func(t *testing.T) {
			desc := driverDescriptor("scriptdb")
			desc.Capabilities = declared
			m := driverOnlyManifest("script-db", desc)
			m.HostABI = "1.0"

			err := registerManifest(t, m, newFakeDriverPlugin())
			require.Error(t, err)
			assert.Contains(t, err.Error(), `extension "script-db"`)
			assert.Contains(t, err.Error(), "databaseDrivers[scriptdb]")
			assert.Contains(t, err.Error(), string(capability))
			assert.Contains(t, err.Error(), "hostABI 1.1")
			_, err = dbregistry.Lookup("scriptdb")
			require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
		})
	}
}

// The same rule on a real guest, through the host's contribution points: the
// fixture driver built for hostABI 1.1 declares both capabilities, so a manifest
// saying 1.0 fails to load, naming the extension and the capability; built as a
// 1.0 extension it loads and its driver is registered without them, so neither
// menu is offered for it.
func TestHostABIOfARealExtensionDriver(t *testing.T) {
	t.Run("a 1.0 manifest over a driver declaring them fails to load", func(t *testing.T) {
		_, err := extensiontest.LoadDBDriverFixtureBuild(t, extensiontest.DBDriverFixtureBuild{ManifestHostABI: "1.0"}, testContributions)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `extension "`+extensiontest.DBDriverFixtureName+`"`)
		assert.Contains(t, err.Error(), string(dbdriver.CapRunSQLFile))
		assert.Contains(t, err.Error(), "hostABI 1.1")
	})

	t.Run("a 1.0 build loads without them", func(t *testing.T) {
		ext, err := extensiontest.LoadDBDriverFixtureBuild(t, extensiontest.DBDriverFixtureBuild{HostABI10: true}, testContributions)
		require.NoError(t, err)
		require.NoError(t, attachExtension(ext))
		t.Cleanup(func() { detach(ext.Name) })
		d, err := dbregistry.Lookup(extensiontest.DBDriverFixtureDriver)
		require.NoError(t, err)
		assert.False(t, d.Descriptor().Supports(dbdriver.CapRunSQLFile))
		assert.False(t, d.Descriptor().Supports(dbdriver.CapDumpSQL))
	})
}

// An extension built for hostABI 1.1 may declare both: its driver is registered
// with them, so the dump and Run SQL File menus are offered for it.
func TestHostABI11DriverMayDeclareTheScriptCapabilities(t *testing.T) {
	desc := driverDescriptor("scriptdb")
	desc.Capabilities = []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapRunSQLFile, dbdriver.CapDumpSQL}
	m := driverOnlyManifest("script-db", desc)
	m.HostABI = "1.1"

	require.NoError(t, registerManifest(t, m, newFakeDriverPlugin()))
	d, err := dbregistry.Lookup("scriptdb")
	require.NoError(t, err)
	assert.True(t, d.Descriptor().Supports(dbdriver.CapRunSQLFile))
	assert.True(t, d.Descriptor().Supports(dbdriver.CapDumpSQL))
}

// A hostABI 1.1 driver that dumps may also declare that its dump files turn
// foreign key checks off for the session: the registered descriptor carries it,
// so the dump dialog offers the option. Declared without dumping it is refused.
func TestHostABI11DriverMayDeclareDumpForeignKeyChecks(t *testing.T) {
	desc := driverDescriptor("fkdumpdb")
	desc.Capabilities = []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapDumpSQL}
	desc.DumpDisablesForeignKeyChecks = true
	m := driverOnlyManifest("fk-dump-db", desc)
	m.HostABI = "1.1"

	require.NoError(t, registerManifest(t, m, newFakeDriverPlugin()))
	d, err := dbregistry.Lookup("fkdumpdb")
	require.NoError(t, err)
	assert.True(t, d.Descriptor().DumpDisablesForeignKeyChecks)

	without := driverDescriptor("fknodumpdb")
	without.DumpDisablesForeignKeyChecks = true
	m = driverOnlyManifest("fk-no-dump-db", without)
	m.HostABI = "1.1"
	err = registerManifest(t, m, newFakeDriverPlugin())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dumpDisablesForeignKeyChecks")
	_, err = dbregistry.Lookup("fknodumpdb")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
}
