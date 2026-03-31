package extension

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// Extension represents a loaded extension.
type Extension struct {
	Name     string
	Dir      string
	Manifest *Manifest
	Plugin   *Plugin
	SkillMD  string // Contents of SKILL.md
}

// Manager handles extension discovery, loading, and lifecycle.
type Manager struct {
	dir        string
	newHost    func(extName string) HostProvider
	logger     *zap.Logger
	mu         sync.RWMutex
	extensions map[string]*Extension
}

func NewManager(dir string, newHost func(extName string) HostProvider, logger *zap.Logger) *Manager {
	return &Manager{
		dir:        dir,
		newHost:    newHost,
		logger:     logger,
		extensions: make(map[string]*Extension),
	}
}

// Scan discovers and loads extensions from the extensions directory.
func (m *Manager) Scan(ctx context.Context) ([]*Manifest, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read extensions dir: %w", err)
	}

	var manifests []*Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		extDir := filepath.Join(m.dir, entry.Name())
		manifest, err := m.LoadExtension(ctx, extDir)
		if err != nil {
			m.logger.Warn("skip extension", zap.String("dir", entry.Name()), zap.Error(err))
			continue
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func (m *Manager) GetExtension(name string) *Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.extensions[name]
}

func (m *Manager) ListExtensions() []*Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	exts := make([]*Extension, 0, len(m.extensions))
	for _, ext := range m.extensions {
		exts = append(exts, ext)
	}
	return exts
}

func (m *Manager) Unload(ctx context.Context, name string) error {
	m.mu.Lock()
	ext, ok := m.extensions[name]
	if ok {
		delete(m.extensions, name)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("extension %q not loaded", name)
	}
	if ext.Plugin != nil {
		return ext.Plugin.Close(ctx)
	}
	return nil
}

func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	exts := m.extensions
	m.extensions = make(map[string]*Extension)
	m.mu.Unlock()
	for _, ext := range exts {
		if ext.Plugin != nil {
			ext.Plugin.Close(ctx)
		}
	}
}

// Watch monitors the extensions directory for changes and reloads.
func (m *Manager) Watch(ctx context.Context, bridge *Bridge, onReload func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}

	if err := os.MkdirAll(m.dir, 0755); err != nil {
		watcher.Close()
		return fmt.Errorf("create extensions dir: %w", err)
	}

	if err := watcher.Add(m.dir); err != nil {
		watcher.Close()
		return fmt.Errorf("watch extensions dir: %w", err)
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Write|fsnotify.Rename) != 0 {
					m.logger.Info("extension directory changed, reloading",
						zap.String("file", event.Name),
						zap.String("op", event.Op.String()))
					m.reload(ctx, bridge)
					if onReload != nil {
						onReload()
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				m.logger.Error("fsnotify error", zap.Error(err))
			}
		}
	}()

	return nil
}

func (m *Manager) reload(ctx context.Context, bridge *Bridge) {
	m.Close(ctx)

	if _, err := m.Scan(ctx); err != nil {
		m.logger.Error("reload scan failed", zap.Error(err))
		return
	}

	for _, name := range bridge.ListNames() {
		bridge.Unregister(name)
	}
	for _, ext := range m.ListExtensions() {
		bridge.Register(ext)
	}

	m.logger.Info("extensions reloaded", zap.Int("count", len(m.ListExtensions())))
}

func (m *Manager) LoadExtension(ctx context.Context, dir string) (*Manifest, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}

	wasmPath := filepath.Join(dir, manifest.Backend.Binary)
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read wasm binary: %w", err)
	}

	skillMD := ""
	if skillData, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err == nil {
		skillMD = string(skillData)
	}

	host := m.newHost(manifest.Name)
	plugin, err := LoadPlugin(ctx, manifest, wasmBytes, host)
	if err != nil {
		host.CloseAll()
		return nil, fmt.Errorf("load plugin: %w", err)
	}

	ext := &Extension{
		Name:     manifest.Name,
		Dir:      dir,
		Manifest: manifest,
		Plugin:   plugin,
		SkillMD:  skillMD,
	}

	m.mu.Lock()
	m.extensions[manifest.Name] = ext
	m.mu.Unlock()

	m.logger.Info("loaded extension", zap.String("name", manifest.Name), zap.String("version", manifest.Version))
	return manifest, nil
}

// ManifestInfo holds manifest data for an extension that may not be loaded.
type ManifestInfo struct {
	Name     string
	Dir      string
	Manifest *Manifest
}

// ScanManifests reads manifests from disk without loading WASM plugins.
func (m *Manager) ScanManifests() ([]*ManifestInfo, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read extensions dir: %w", err)
	}

	var result []*ManifestInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		extDir := filepath.Join(m.dir, entry.Name())
		manifestPath := filepath.Join(extDir, "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		manifest, err := ParseManifest(data)
		if err != nil {
			continue
		}
		result = append(result, &ManifestInfo{
			Name:     manifest.Name,
			Dir:      extDir,
			Manifest: manifest,
		})
	}
	return result, nil
}

// Install installs an extension from a zip file or directory.
func (m *Manager) Install(ctx context.Context, sourcePath string) (*Manifest, error) {
	sourceDir := sourcePath
	var tmpDir string

	// If zip, extract to temp directory
	if strings.HasSuffix(strings.ToLower(sourcePath), ".zip") {
		var err error
		tmpDir, err = os.MkdirTemp("", "opskat-ext-*")
		if err != nil {
			return nil, fmt.Errorf("create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		if err := extractZip(sourcePath, tmpDir); err != nil {
			return nil, fmt.Errorf("extract zip: %w", err)
		}
		sourceDir = tmpDir
	}

	// Read and validate manifest
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}

	// Unload existing if already loaded
	m.mu.RLock()
	_, exists := m.extensions[manifest.Name]
	m.mu.RUnlock()
	if exists {
		if err := m.Unload(ctx, manifest.Name); err != nil {
			m.logger.Warn("unload existing extension", zap.String("name", manifest.Name), zap.Error(err))
		}
	}

	// Copy to extensions directory
	destDir := filepath.Join(m.dir, manifest.Name)
	if err := os.RemoveAll(destDir); err != nil {
		return nil, fmt.Errorf("remove existing dir: %w", err)
	}
	if err := copyDir(sourceDir, destDir); err != nil {
		return nil, fmt.Errorf("copy extension: %w", err)
	}

	// Load the extension
	if _, err := m.LoadExtension(ctx, destDir); err != nil {
		os.RemoveAll(destDir)
		return nil, fmt.Errorf("load extension: %w", err)
	}

	return manifest, nil
}

// Uninstall stops and removes an extension from disk.
func (m *Manager) Uninstall(ctx context.Context, name string) error {
	// Unload if loaded (ignore error if not loaded)
	_ = m.Unload(ctx, name)

	// Remove extension directory
	extDir := filepath.Join(m.dir, name)
	if err := os.RemoveAll(extDir); err != nil {
		return fmt.Errorf("remove extension dir: %w", err)
	}
	return nil
}

// ExtDir returns the path to a named extension's directory.
func (m *Manager) ExtDir(name string) string {
	return filepath.Join(m.dir, name)
}
