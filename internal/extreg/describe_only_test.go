package extreg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
)

// An extension that provides database drivers and nothing else is a complete
// describe-only registration: its drivers are known (descriptor, fields) in this
// process, and the registration can be replaced by the full one on activation.
func TestRegisterDescribeOnly_DriverOnlyExtensionRegistersItsDrivers(t *testing.T) {
	desc := driverDescriptor("describedb", "dsc")
	m := driverOnlyManifest("describe-only-drivers", desc)
	require.NoError(t, RegisterDescribeOnly(&extension.ManifestInfo{Name: m.Name, Manifest: m}))
	t.Cleanup(func() { Unregister(m.Name) })

	d, err := dbregistry.Lookup("describedb")
	require.NoError(t, err)
	assert.Equal(t, desc.Name, d.Descriptor().Name)
	byAlias, ok := dbregistry.ByName("dsc")
	require.True(t, ok)
	assert.Equal(t, d.Descriptor().ID, byAlias.Descriptor().ID)

	owner, ok := DriverOwnerOf("describedb")
	require.True(t, ok)
	assert.Equal(t, m.Name, owner)

	// Not runnable: nothing was loaded, so opening says so instead of crashing.
	_, err = d.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "describedb"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)

	// Unregister is what activation does before the full registration.
	Unregister(m.Name)
	_, err = dbregistry.Lookup("describedb")
	assert.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	_, ok = DriverOwnerOf("describedb")
	assert.False(t, ok)
}

func TestRegisterDescribeOnly_DriverNameClashRegistersNothing(t *testing.T) {
	m := driverOnlyManifest("describe-only-clash", driverDescriptor("describeclash"), driverDescriptor("mysql"))
	err := RegisterDescribeOnly(&extension.ManifestInfo{Name: m.Name, Manifest: m})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mysql")
	_, lookupErr := dbregistry.Lookup("describeclash")
	assert.ErrorIs(t, lookupErr, dbregistry.ErrDriverUnavailable, "all or nothing")
}

func TestRegisterDescribeOnly_NothingDescribedIsStillRejected(t *testing.T) {
	m := &extension.Manifest{Name: "describe-only-empty", Version: "1.0.0"}
	err := RegisterDescribeOnly(&extension.ManifestInfo{Name: m.Name, Manifest: m})
	require.Error(t, err)
}
