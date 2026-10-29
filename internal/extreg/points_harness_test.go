package extreg

import (
	"encoding/json"
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

// testView is an extension as its contribution points see it: the runtime's
// manifest — contract and raw describe() entries — and, standing in for its
// running guest, the fake plugin l carries.
type testView struct {
	l                  loaded
	rt                 *extension.Manifest
	skillMD, skillDesc string
}

func (v *testView) Name() string                            { return v.l.name }
func (v *testView) Manifest() *extension.Manifest           { return v.rt }
func (v *testView) Plugin() *extension.Plugin               { return nil }
func (v *testView) SkillMD() string                         { return v.skillMD }
func (v *testView) SkillDescription() string                { return v.skillDesc }
func (v *testView) Keys() []string                          { return v.rt.ContributionKeys() }
func (v *testView) Contribution(key string) json.RawMessage { return v.rt.Contribution(key) }

func (v *testView) Translate(lang, key string) string {
	if v.l.translate == nil {
		return key
	}
	return v.l.translate(lang, key)
}

// attachLoaded attaches the extension l describes, running l.plugin (nil: a
// describe-only attach). The hand-written typed view is encoded back into the
// describe() answer it stands for, which the contribution points judge and
// decode like a real one.
func attachLoaded(l loaded, skillMD, description string) error {
	return testContributions.Attach(&testView{l: l, rt: runtimeManifest(l.manifest), skillMD: skillMD, skillDesc: description})
}

// attachExtension attaches an extension the way extension_svc does. A real loaded
// one (a fixture) is attached through its own view; one a test assembles by hand
// has no running guest and attaches describe-only.
func attachExtension(ext *extension.Extension) error {
	if ext.Plugin != nil {
		return testContributions.Attach(ext.View())
	}
	return testContributions.Attach(&testView{l: loaded{name: ext.Name, translate: ext.Translate}, rt: ext.Manifest,
		skillMD: ext.SkillMD, skillDesc: ext.SkillDescription})
}

// attachDescribeOnly attaches an extension read from the describe cache, the way
// opsctl does at startup.
func attachDescribeOnly(info *extension.ManifestInfo) error {
	return testContributions.Attach(&testView{l: loaded{name: info.Name, translate: info.Translate}, rt: info.Manifest,
		skillMD: info.SkillMD, skillDesc: info.SkillDescription})
}

// runtimeManifest is the runtime's manifest a hand-written typed view stands for:
// its contract and presentation, and the describe() entries it was decoded from.
func runtimeManifest(m *Manifest) *extension.Manifest {
	rt := m.Manifest
	if rt == nil {
		rt = &extension.Manifest{}
	}
	return extension.Described(rt.Manifest, &extension.Descriptor{Icon: rt.Icon, I18n: rt.I18n, Contributes: wireContributions(m)})
}

// wireContributions is the describe() answer a hand-written typed view stands
// for: the entries its face was decoded from.
func wireContributions(m *Manifest) map[string]json.RawMessage {
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
				Connection: at.Connection, Auth: wireAuth(at.Auth), TestConnection: at.TestConnection}
		}
		put(extwire.KeyAssetTypes, types)
	}
	if len(m.Tools) > 0 {
		put(extwire.KeyTools, m.Tools)
	}
	if m.Policies.Type != "" || len(m.Policies.Groups) > 0 {
		put(extwire.KeyPolicy, extwire.Policy{Type: m.Policies.Type, Groups: m.Policies.Groups, Default: m.Policies.Default})
	}
	if m.Frontend.Entry != "" || len(m.Frontend.Pages) > 0 {
		put(extwire.KeyPages, m.Frontend)
	}
	if len(m.Snippets.Categories) > 0 || len(m.Snippets.Seed) > 0 {
		put(extwire.KeySnippets, m.Snippets)
	}
	if len(m.DatabaseDrivers) > 0 {
		put(extwire.KeyDatabaseDrivers, m.DatabaseDrivers)
	}
	return raw
}

func wireAuth(a *AuthDef) *extwire.Auth {
	if a == nil {
		return nil
	}
	out := &extwire.Auth{Selector: a.Selector, Groups: make([]extwire.AuthGroup, len(a.Groups))}
	for i, g := range a.Groups {
		out.Groups[i] = extwire.AuthGroup{When: g.When, Bindings: make([]extwire.AuthBinding, len(g.Bindings))}
		for j, b := range g.Bindings {
			out.Groups[i].Bindings[j] = extwire.AuthBinding(b)
		}
	}
	return out
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
		l:  loaded{name: mf.Name},
		rt: extension.Described(mf, &extension.Descriptor{Contributes: w.Contributes}),
	})
}
