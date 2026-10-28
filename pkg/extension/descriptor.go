package extension

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extwire"
)

// Descriptor is what the guest answers to describe(): everything an extension can
// do, derived inside the guest from the registration calls themselves.
//
// Declaring these in manifest.json would be a second declaration language that
// nothing checks against the code. The manifest keeps what a user must be able to
// audit *before* running the code — the capability grants — and the rest is read
// back from the code that implements it.
//
// The wire shape is pkg/extwire's — the one the guest SDK encodes — decoded here
// contribution by contribution. Nothing here judges it: the rules belong to the
// contribution points (ContributionPoint) of the subsystems that consume each
// contribution.
type Descriptor struct {
	Icon       string
	I18n       ManifestI18n
	AssetTypes []AssetTypeDef
	Tools      []ToolDef
	Policy     extwire.Policy
	Frontend   FrontendDef
	Snippets   SnippetsDef
	// DatabaseDrivers are the SQL database drivers the extension provides, one
	// descriptor per id the manifest lists in contributes.databaseDrivers.
	DatabaseDrivers []dbdriver.Descriptor
	// contributes is the raw answer, one entry per contribution point, for the
	// points to judge (ExtensionView.Contribution).
	contributes map[string]json.RawMessage
}

// ParseDescriptor decodes a describe() payload without judging it. A payload that
// does not decode is not an answer in this host's wire shape at all — the describe
// cache treats that as stale — while one that decodes is judged contribution by
// contribution by the points of the process's ContributionRegistry once it is
// merged onto the manifest.
func ParseDescriptor(data []byte) (*Descriptor, error) {
	w, err := extwire.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse describe() result: %w", err)
	}
	d := &Descriptor{Icon: w.Icon, I18n: w.I18n, contributes: w.Contributes}
	var assetTypes []extwire.AssetType
	for key, dst := range map[string]any{
		extwire.KeyAssetTypes:      &assetTypes,
		extwire.KeyTools:           &d.Tools,
		extwire.KeyPolicy:          &d.Policy,
		extwire.KeyPages:           &d.Frontend,
		extwire.KeySnippets:        &d.Snippets,
		extwire.KeyDatabaseDrivers: &d.DatabaseDrivers,
	} {
		if err := w.Get(key, dst); err != nil {
			return nil, fmt.Errorf("parse describe() result: %w", err)
		}
	}
	for _, at := range assetTypes {
		d.AssetTypes = append(d.AssetTypes, assetTypeDef(at))
	}
	return d, nil
}

// apply merges the guest's functional face onto the manifest's security face.
//
// policies.actions is not on the wire: it is exactly the set of actions the tools
// request, so deriving it here keeps the tool registrations the only place an
// action is declared.
func (m *Manifest) apply(d *Descriptor) {
	m.Icon = d.Icon
	m.I18n = d.I18n
	m.AssetTypes = d.AssetTypes
	m.Tools = d.Tools
	m.Policies = PoliciesDef{
		Type:    d.Policy.Type,
		Actions: policyActions(d.Tools),
		Groups:  d.Policy.Groups,
		Default: d.Policy.Default,
	}
	m.Frontend = d.Frontend
	m.Snippets = d.Snippets
	m.contributes = d.contributes
	// A driver's icon is the extension's: it is the one icon the user sees on it.
	m.DatabaseDrivers = make([]dbdriver.Descriptor, len(d.DatabaseDrivers))
	for i, drv := range d.DatabaseDrivers {
		drv.Icon = d.Icon
		m.DatabaseDrivers[i] = drv
	}
}

func policyActions(tools []ToolDef) []string {
	seen := make(map[string]struct{}, len(tools))
	actions := make([]string, 0, len(tools))
	for _, t := range tools {
		for _, a := range t.Actions() {
			if _, dup := seen[a]; dup {
				continue
			}
			seen[a] = struct{}{}
			actions = append(actions, a)
		}
	}
	sort.Strings(actions)
	return actions
}
