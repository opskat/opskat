package extension

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// ContributionPoint is one key of describe()'s contributes — asset types, tools,
// snippets, … — owned by the host subsystem that consumes it. The runtime knows
// none of their rules: the point judges its own entry (Check) and wires it into
// its subsystem (Attach); the runtime only routes every key to its point.
//
// A point is handed the whole extension (ExtensionView), not just its own entry,
// because several rules cross contributions: auth on an asset type needs the
// manifest's network.assetEndpoint, policy groups are namespaced by the policy
// type, drivers in describe() must be the ones the manifest declares.
type ContributionPoint interface {
	// Key is the contributes key the point owns.
	Key() string
	// Check judges the extension's contribution to this point; it is called for
	// every extension, whether it contributes here or not, so a point can refuse
	// a contribution that is missing. Its error names the contribution and the
	// item (e.g. `tools[bulk]: …`); the registry adds the extension.
	Check(v ExtensionView) error
	// Attach wires the (checked) contribution into the subsystem and returns how
	// to take it out again. On error it has attached nothing.
	Attach(v ExtensionView) (revoke func(), err error)
}

// ExtensionView is the read-only view of one extension a contribution point
// judges and attaches: its security contract and every contribution, as raw
// describe() entries. The typed view of the contributions is the host's
// (internal/extreg), decoded from these entries.
type ExtensionView interface {
	Name() string
	// Manifest is manifest.json's security contract with describe()'s
	// presentation and raw contributions merged onto it.
	Manifest() *Manifest
	// Keys is the contributes keys describe() answered, sorted.
	Keys() []string
	// Contribution is the raw describe() entry under key, nil when absent.
	Contribution(key string) json.RawMessage
	// Plugin is the running guest; nil when the view was read from the describe
	// cache without running the extension (opsctl, the extension list).
	Plugin() *Plugin
	// Translate resolves one of the extension's i18n keys.
	Translate(lang, key string) string
	// SkillMD is the body of the extension's SKILL.md, SkillDescription its
	// frontmatter description; both empty when it ships none.
	SkillMD() string
	SkillDescription() string
}

// extensionView is the one ExtensionView implementation: a loaded extension
// (Extension.View) or a cached one (ManifestInfo.View).
type extensionView struct {
	manifest         *Manifest
	plugin           *Plugin
	locales          map[string]map[string]string
	skillMD          string
	skillDescription string
}

func newExtensionView(m *Manifest, plugin *Plugin, locales map[string]map[string]string, skillMD, skillDescription string) *extensionView {
	return &extensionView{manifest: m, plugin: plugin, locales: locales, skillMD: skillMD, skillDescription: skillDescription}
}

func (v *extensionView) Name() string        { return v.manifest.Name }
func (v *extensionView) Manifest() *Manifest { return v.manifest }
func (v *extensionView) Plugin() *Plugin     { return v.plugin }

func (v *extensionView) Keys() []string { return v.manifest.ContributionKeys() }

func (v *extensionView) Contribution(key string) json.RawMessage {
	return v.manifest.Contribution(key)
}

func (v *extensionView) Translate(lang, key string) string {
	return translateFromLocales(v.locales, lang, key)
}

func (v *extensionView) SkillMD() string          { return v.skillMD }
func (v *extensionView) SkillDescription() string { return v.skillDescription }

// View is the loaded extension as its contribution points see it.
func (e *Extension) View() ExtensionView {
	return newExtensionView(e.Manifest, e.Plugin, e.Locales, e.SkillMD, e.SkillDescription)
}

// View is the cached extension as its contribution points see it; it has no
// running guest.
func (mi *ManifestInfo) View() ExtensionView {
	return newExtensionView(mi.Manifest, nil, mi.Locales, mi.SkillMD, mi.SkillDescription)
}

// ContributionRegistry routes every contributes key of an extension to the point
// registered for it. A process that runs extensions (the desktop app, opsctl)
// builds one, with the same points, before it loads any extension.
//
// An extension is judged by every point, and loads only if all of them accept
// it; a key no point is registered for — one this host does not know, or one
// whose point this process did not register — refuses it. A load, a cached
// describe() read and an attach all run the same Check, so they cannot disagree.
type ContributionRegistry struct {
	points []ContributionPoint
	keys   map[string]struct{}

	mu sync.Mutex
	// attached maps an extension name to the revokes of its points, in attach
	// order; a nil slice marks an attach in progress.
	attached map[string][]func()
}

// NewContributionRegistry builds a registry of points. Two points claiming one key
// is a wiring error and panics.
func NewContributionRegistry(points ...ContributionPoint) *ContributionRegistry {
	r := &ContributionRegistry{
		points:   points,
		keys:     make(map[string]struct{}, len(points)),
		attached: make(map[string][]func()),
	}
	for _, p := range points {
		if _, dup := r.keys[p.Key()]; dup {
			panic(fmt.Sprintf("extension: two contribution points claim contributes key %q", p.Key()))
		}
		r.keys[p.Key()] = struct{}{}
	}
	return r
}

// Check judges v: every contributes key must have a point, and every point must
// accept the extension. The error names the extension first.
func (r *ContributionRegistry) Check(v ExtensionView) error {
	for _, key := range v.Keys() {
		if _, ok := r.keys[key]; !ok {
			return fmt.Errorf("%s: contributes.%s: no contribution point for it is registered in this host", v.Name(), key)
		}
	}
	for _, p := range r.points {
		if err := p.Check(v); err != nil {
			return fmt.Errorf("%s: %w", v.Name(), err)
		}
	}
	return nil
}

// Attach checks v and attaches it to every point, all or nothing: when a point
// fails, the points already attached are revoked and the error returned. An
// extension stays attached, under its name, until Revoke.
func (r *ContributionRegistry) Attach(v ExtensionView) error {
	if err := r.Check(v); err != nil {
		return err
	}
	name := v.Name()
	r.mu.Lock()
	if _, taken := r.attached[name]; taken {
		r.mu.Unlock()
		return fmt.Errorf("%s: already attached", name)
	}
	r.attached[name] = nil
	r.mu.Unlock()

	revokes := make([]func(), 0, len(r.points))
	for _, p := range r.points {
		revoke, err := p.Attach(v)
		if err != nil {
			revokeAll(revokes)
			r.mu.Lock()
			delete(r.attached, name)
			r.mu.Unlock()
			return fmt.Errorf("%s: %w", name, err)
		}
		revokes = append(revokes, revoke)
	}
	r.mu.Lock()
	r.attached[name] = revokes
	r.mu.Unlock()
	logger.Default().Info("extension contributions attached",
		zap.String("extension", name), zap.Strings("contributes", v.Keys()), zap.Bool("running", v.Plugin() != nil))
	return nil
}

// Revoke takes every contribution of the extension named name out of its
// subsystem, in reverse attach order. A name not attached is a no-op.
func (r *ContributionRegistry) Revoke(name string) {
	r.mu.Lock()
	revokes, ok := r.attached[name]
	if ok && revokes != nil {
		delete(r.attached, name)
	}
	r.mu.Unlock()
	if revokes == nil {
		return
	}
	revokeAll(revokes)
	logger.Default().Info("extension contributions revoked", zap.String("extension", name))
}

func revokeAll(revokes []func()) {
	for i := len(revokes) - 1; i >= 0; i-- {
		revokes[i]()
	}
}
