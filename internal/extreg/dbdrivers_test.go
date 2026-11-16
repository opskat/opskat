package extreg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 扩展驱动还不能经 ABI 运行 SQL 文件：声明了这项能力的扩展加载时被拒绝，驱动不进注册表。
func TestExtensionDriverDeclaringRunSQLFileIsRefused(t *testing.T) {
	desc := driverDescriptor("scriptdb")
	desc.Capabilities = []dbdriver.Capability{dbdriver.CapRunSQLFile}

	err := registerManifest(t, driverOnlyManifest("script-db", desc), newFakeDriverPlugin())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "databaseDrivers[scriptdb]")
	assert.Contains(t, err.Error(), string(dbdriver.CapRunSQLFile))
	_, err = dbregistry.Lookup("scriptdb")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
}

// 扩展驱动还不能经 ABI 转储：声明了这项能力的扩展加载时被拒绝，驱动不进注册表。
func TestExtensionDriverDeclaringDumpSQLIsRefused(t *testing.T) {
	desc := driverDescriptor("dumpdb")
	desc.Capabilities = []dbdriver.Capability{dbdriver.CapViewDDL, dbdriver.CapDumpSQL}

	err := registerManifest(t, driverOnlyManifest("dump-db", desc), newFakeDriverPlugin())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "databaseDrivers[dumpdb]")
	assert.Contains(t, err.Error(), string(dbdriver.CapDumpSQL))
	_, err = dbregistry.Lookup("dumpdb")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
}
