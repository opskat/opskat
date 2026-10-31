package extensiontest

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

// Describe is a describe() answer a test writes by hand: the presentation and,
// under Contributes, each contribution point key's entry in its wire type
// (extwire.KeyAssetTypes → []extwire.AssetType, …).
type Describe struct {
	Icon        string
	I18n        extension.ManifestI18n
	Contributes map[string]any
}

// Described is the runtime's manifest of an extension whose manifest.json says
// contract and whose describe() answers d — what loading it produces, for a test
// of a subsystem that reads an extension's contributions without running one. A
// contribution that does not encode is the test's own mistake and panics.
func Described(contract extmanifest.Manifest, d Describe) *extension.Manifest {
	raw := make(map[string]json.RawMessage, len(d.Contributes))
	for key, v := range d.Contributes {
		b, err := json.Marshal(v)
		if err != nil {
			panic(fmt.Sprintf("extensiontest: encode contributes.%s: %v", key, err))
		}
		raw[key] = b
	}
	return extension.Described(contract, &extension.Descriptor{Icon: d.Icon, I18n: d.I18n, Contributes: raw})
}

// DatabaseDrivers is the driver descriptors m's describe() contributes, as the
// host's typed view reads them (internal/extreg ManifestOf): a driver's icon is
// its extension's.
func DatabaseDrivers(t testing.TB, m *extension.Manifest) []dbdriver.Descriptor {
	t.Helper()
	var drivers []dbdriver.Descriptor
	if raw := m.Contribution(extwire.KeyDatabaseDrivers); raw != nil {
		if err := json.Unmarshal(raw, &drivers); err != nil {
			t.Fatalf("extensiontest: decode contributes.databaseDrivers: %v", err)
		}
	}
	for i := range drivers {
		drivers[i].Icon = m.Icon
	}
	return drivers
}
