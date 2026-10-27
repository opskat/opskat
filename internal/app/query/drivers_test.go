package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
)

// 扩展提供的驱动答出扩展名（设计器据此给「去更新」入口）；内置驱动与未注册的 id 答空串。
func TestDatabaseDriverOwnerNamesTheProvidingExtension(t *testing.T) {
	desc := dbdriver.Descriptor{
		ID: "ownerdb", Name: "OwnerDB", Icon: "database", Order: 500, DefaultPort: 9000,
		Form: []dbdriver.FormTabFields{{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldDatabase,
		}}},
		Capabilities: []dbdriver.Capability{},
	}
	m := &extension.Manifest{Name: "owner-ext", Version: "1.0.0", DatabaseDrivers: []dbdriver.Descriptor{desc}}
	m.Capabilities.DatabaseDrivers = []string{string(desc.ID)}
	require.NoError(t, extreg.RegisterDescribeOnly(&extension.ManifestInfo{Name: m.Name, Manifest: m}))
	t.Cleanup(func() { extreg.Unregister(m.Name) })

	q := &Query{}
	assert.Equal(t, "owner-ext", q.DatabaseDriverOwner("ownerdb"))
	assert.Equal(t, "", q.DatabaseDriverOwner("mysql"))
	assert.Equal(t, "", q.DatabaseDriverOwner("nosuchdb"))
}
