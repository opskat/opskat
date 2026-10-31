package extension

import (
	"sync"
)

// Bridge is the lifecycle table of currently loaded extensions: name → *Extension.
//
// It is deliberately not a registry: asset types, policy groups, default policies,
// SKILL.md bodies and tools go through internal/extreg into the same registries
// built-in asset types use, so no consumer has to ask "built in, or extension?". The
// bridge keeps only what is genuinely its own: which extensions are loaded right now.
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
