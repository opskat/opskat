package extension

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extwire"
)

// face is what an extension's raw contributions say, decoded with the wire types
// the guest SDK encodes them with. The runtime never decodes them — the host's
// contribution points do (internal/extreg, which these runtime tests cannot
// import) — so a test that observes what a guest described reads it this way.
type face struct {
	AssetTypes      []extwire.AssetType
	Tools           []extwire.Tool
	Policy          extwire.Policy
	DatabaseDrivers []dbdriver.Descriptor
}

func faceOf(t testing.TB, m *Manifest) face {
	t.Helper()
	f, err := decodeFace(m)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func decodeFace(m *Manifest) (face, error) {
	var f face
	for key, dst := range map[string]any{
		extwire.KeyAssetTypes:      &f.AssetTypes,
		extwire.KeyTools:           &f.Tools,
		extwire.KeyPolicy:          &f.Policy,
		extwire.KeyDatabaseDrivers: &f.DatabaseDrivers,
	} {
		if raw := m.Contribution(key); raw != nil {
			if err := json.Unmarshal(raw, dst); err != nil {
				return face{}, fmt.Errorf("decode contributes.%s: %w", key, err)
			}
		}
	}
	return f, nil
}

// assetType is the declaration of the asset type named name, nil when none.
func (f face) assetType(name string) *extwire.AssetType {
	for i := range f.AssetTypes {
		if f.AssetTypes[i].Type == name {
			return &f.AssetTypes[i]
		}
	}
	return nil
}

// toolNames is the described tools' names.
func (f face) toolNames() []string {
	names := make([]string, 0, len(f.Tools))
	for _, tool := range f.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// actions is every policy action the described tools request, sorted.
func (f face) actions() []string {
	var out []string
	for _, tool := range f.Tools {
		for _, a := range tool.Actions() {
			if !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	slices.Sort(out)
	return out
}
