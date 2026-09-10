package extension

import (
	"sync"
)

// Bridge is the lifecycle table of currently loaded extensions: name → *Extension.
//
// It used to be a second, parallel registry — asset types, policy groups, default
// policies, SKILL.md bodies and a tool index all lived here, in shapes the app's own
// registries already had. Every consumer therefore had to ask twice ("is it built in?
// no? then is it an extension?"), which is the type-string branching the registries
// exist to remove. Those registrations now go through internal/extreg into the same
// registries built-in asset types use, and the bridge keeps only the one thing that is
// genuinely its own: which extensions are loaded right now.
type Bridge struct {
	mu         sync.RWMutex
	extensions map[string]*Extension
}

func NewBridge() *Bridge {
	return &Bridge{extensions: make(map[string]*Extension)}
}

// Register records a loaded extension. Wiring it into the app registries is
// internal/extreg's job and must succeed before this is called — a bridge entry means
// "loaded and reachable", and there is no half-registered state to represent.
func (b *Bridge) Register(ext *Extension) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.extensions[ext.Name] = ext
}

func (b *Bridge) Unregister(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.extensions, name)
}

func (b *Bridge) ListNames() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.extensions))
	for name := range b.extensions {
		names = append(names, name)
	}
	return names
}

// Get returns a loaded extension by name, or nil.
func (b *Bridge) Get(name string) *Extension {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.extensions[name]
}

// GetExtensionByAssetType returns the Extension that owns the given asset type, or nil.
// The mapping is one-to-one by construction: registering an asset type a built-in type
// or another extension already owns is refused at load time (internal/extreg).
func (b *Bridge) GetExtensionByAssetType(assetType string) *Extension {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ext := range b.extensions {
		for _, at := range ext.Manifest.AssetTypes {
			if at.Type == assetType {
				return ext
			}
		}
	}
	return nil
}
