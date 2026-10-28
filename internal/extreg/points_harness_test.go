package extreg

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

// testContributions is the host's contribution points behind one registry, as a
// process builds them (Points), except that a testView's extension runs the fake
// plugin it carries instead of a WASM guest.
var testContributions = extension.NewContributionRegistry(newPoints(func(v extension.ExtensionView) pluginCaller {
	if tv, ok := v.(*testView); ok {
		return tv.l.plugin
	}
	return pluginOf(v)
})...)

// testView is an extension as its contribution points see it, built from a manifest
// a test writes by hand (the merged face, what Attach reads) and, for Check, the raw
// describe() entries.
type testView struct {
	l                  loaded
	raw                map[string]json.RawMessage
	skillMD, skillDesc string
}

func (v *testView) Name() string                  { return v.l.name }
func (v *testView) Manifest() *extension.Manifest { return v.l.manifest }
func (v *testView) Plugin() *extension.Plugin     { return nil }
func (v *testView) SkillMD() string               { return v.skillMD }
func (v *testView) SkillDescription() string      { return v.skillDesc }

func (v *testView) Keys() []string {
	keys := make([]string, 0, len(v.raw))
	for k := range v.raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (v *testView) Contribution(key string) json.RawMessage { return v.raw[key] }

func (v *testView) Translate(lang, key string) string {
	if v.l.translate == nil {
		return key
	}
	return v.l.translate(lang, key)
}

// attachLoaded attaches the extension l describes, running l.plugin (nil: a
// describe-only attach). The hand-written manifest's face is what describe() would
// have answered, so the contribution points judge it before attaching.
func attachLoaded(l loaded, skillMD, description string) error {
	return testContributions.Attach(&testView{l: l, raw: wireContributions(l.manifest), skillMD: skillMD, skillDesc: description})
}

// attachExtension attaches an extension the way extension_svc does. A real loaded
// one (a fixture) is attached through its own view; one a test assembles by hand
// has no running guest and attaches describe-only.
func attachExtension(ext *extension.Extension) error {
	if ext.Plugin != nil {
		return testContributions.Attach(ext.View())
	}
	return attachLoaded(loaded{name: ext.Name, manifest: ext.Manifest, translate: ext.Translate}, ext.SkillMD, ext.SkillDescription)
}

// attachDescribeOnly attaches an extension read from the describe cache, the way
// opsctl does at startup.
func attachDescribeOnly(info *extension.ManifestInfo) error {
	return attachLoaded(loaded{name: info.Name, manifest: info.Manifest, translate: info.Translate}, info.SkillMD, info.SkillDescription)
}

// wireContributions is the describe() answer a hand-written merged manifest stands
// for: the entries its face was merged from.
func wireContributions(m *extension.Manifest) map[string]json.RawMessage {
	raw := map[string]json.RawMessage{}
	put := func(key string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		raw[key] = b
	}
	if len(m.AssetTypes) > 0 {
		types := make([]extwire.AssetType, len(m.AssetTypes))
		for i, at := range m.AssetTypes {
			types[i] = extwire.AssetType{Type: at.Type, I18n: at.I18n, ConfigSchema: at.ConfigSchema,
				Connection: at.Connection, TestConnection: at.TestConnection}
		}
		put(extwire.KeyAssetTypes, types)
	}
	if len(m.Tools) > 0 {
		put(extwire.KeyTools, m.Tools)
	}
	if m.Policies.Type != "" || len(m.Policies.Groups) > 0 {
		put(extwire.KeyPolicy, extwire.Policy{Type: m.Policies.Type, Groups: m.Policies.Groups, Default: m.Policies.Default})
	}
	if len(m.DatabaseDrivers) > 0 {
		put(extwire.KeyDatabaseDrivers, m.DatabaseDrivers)
	}
	return raw
}

// detach revokes everything the extension named name attached.
func detach(name string) { testContributions.Revoke(name) }

// checkDescribe judges a describe() payload against the security contract mf with
// the host's contribution points — what loading the extension, or reading its
// cached answer, concludes.
func checkDescribe(t *testing.T, mf extmanifest.Manifest, payload []byte) error {
	t.Helper()
	w, err := extwire.Parse(payload)
	require.NoError(t, err)
	if mf.Name == "" {
		mf.Name = "ext"
	}
	return testContributions.Check(&testView{
		l:   loaded{name: mf.Name, manifest: &extension.Manifest{Manifest: mf}},
		raw: w.Contributes,
	})
}
