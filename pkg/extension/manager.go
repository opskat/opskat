package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

// Extension represents a loaded extension.
type Extension struct {
	Name             string
	Dir              string
	Manifest         *Manifest
	Plugin           *Plugin
	SkillMD          string                       // Body of SKILL.md (frontmatter stripped)
	SkillDescription string                       // description from SKILL.md frontmatter, if any
	Locales          map[string]map[string]string // lang → key → translated text
}

// Translate resolves an i18n key for the given language.
// Falls back to "en", then returns the key itself.
func (e *Extension) Translate(lang, key string) string {
	return translateFromLocales(e.Locales, lang, key)
}

// translateFromLocales resolves an i18n key from a locales map.
func translateFromLocales(locales map[string]map[string]string, lang, key string) string {
	if locales != nil {
		if m, ok := locales[lang]; ok {
			if v, ok := m[key]; ok {
				return v
			}
		}
		if m, ok := locales["en"]; ok {
			if v, ok := m[key]; ok {
				return v
			}
		}
	}
	return key
}

// Translate resolves an i18n key for the given language.
func (mi *ManifestInfo) Translate(lang, key string) string {
	return translateFromLocales(mi.Locales, lang, key)
}

// LoadLocales reads all JSON files from the extension's locales/ directory.
// Language codes are normalized to lowercase for consistent matching (e.g. "zh-CN" → "zh-cn").
// The directory is optional; a locale file that cannot be read or parsed is an
// authoring error and is returned naming the file, not skipped — a skipped file
// only shows up as raw i18n keys in the UI.
func LoadLocales(dir string) (map[string]map[string]string, error) {
	localesDir := filepath.Join(dir, "locales")
	entries, err := os.ReadDir(localesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read locales dir: %w", err)
	}
	result := make(map[string]map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		lang := strings.ToLower(strings.TrimSuffix(entry.Name(), ".json"))
		data, err := os.ReadFile(filepath.Join(localesDir, entry.Name())) //nolint:gosec // path constructed from ReadDir within known locales directory
		if err != nil {
			return nil, fmt.Errorf("read locale file locales/%s: %w", entry.Name(), err)
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse locale file locales/%s: %w", entry.Name(), err)
		}
		result[lang] = m
	}
	return result, nil
}

// localesInfo is LoadLocales for the no-runtime readers, which list extensions
// rather than run them: a broken locale file is reported at error level and the
// extension is listed untranslated, mirroring skillMDInfo.
func localesInfo(dir, extName string) map[string]map[string]string {
	locales, err := LoadLocales(dir)
	if err != nil {
		logger.Default().Error("load extension locales",
			zap.String("extension", extName), zap.Error(err))
		return nil
	}
	return locales
}

// Manager handles extension discovery, loading, and lifecycle.
type Manager struct {
	dir          string
	newHost      func(extName string) HostProvider
	logger       *zap.Logger
	mu           sync.RWMutex
	extensions   map[string]*Extension
	wasmCache    wazero.CompilationCache
	installMu    sync.Mutex
	installLocks map[string]*sync.Mutex
}

func NewManager(dir string, newHost func(extName string) HostProvider, logger *zap.Logger) *Manager {
	cacheDir := filepath.Join(dir, ".cache")
	cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		logger.Warn("failed to create wasm compilation cache, will compile without cache", zap.Error(err))
	}
	return &Manager{
		dir:          dir,
		newHost:      newHost,
		logger:       logger,
		extensions:   make(map[string]*Extension),
		wasmCache:    cache,
		installLocks: make(map[string]*sync.Mutex),
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
		if !isExtensionDir(entry) {
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
			if err := ext.Plugin.Close(ctx); err != nil {
				logger.Default().Warn("close extension plugin", zap.String("name", ext.Name), zap.Error(err))
			}
		}
	}
	// wasmCache 不在这里关闭 — Close 也用于 Reload，缓存需要跨 reload 复用
}

// Shutdown closes all extensions and releases the compilation cache.
func (m *Manager) Shutdown(ctx context.Context) {
	m.Close(ctx)
	if m.wasmCache != nil {
		if err := m.wasmCache.Close(ctx); err != nil {
			logger.Default().Warn("close wasm compilation cache", zap.Error(err))
		}
	}
}

// LoadManifestInfo reads an extension directory without compiling or running its
// WASM module: the manifest gives the security contract, the descriptor cache gives
// the functional face. It is the entry point for callers that have no runtime at
// all (opsctl) or that are looking at an extension which is not loaded.
//
// A cache miss leaves the functional face empty — that only happens for an
// extension this machine has never loaded, since every load populates the cache.
func LoadManifestInfo(dir string) (*ManifestInfo, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath) //nolint:gosec // extension directories are trusted
	if err != nil {
		return nil, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	applyCachedDescriptor(manifest, dir)
	return newManifestInfo(manifest, dir), nil
}

// newManifestInfo assembles the no-runtime view of one extension directory.
func newManifestInfo(manifest *Manifest, dir string) *ManifestInfo {
	skillMD, skillDescription := skillMDInfo(dir, manifest.Name)
	return &ManifestInfo{
		Name:             manifest.Name,
		Dir:              dir,
		Manifest:         manifest,
		Locales:          localesInfo(dir, manifest.Name),
		SkillMD:          skillMD,
		SkillDescription: skillDescription,
	}
}

// applyCachedDescriptor merges the cached describe() answer for the extension in
// dir, if the cache holds one for the wasm binary currently on disk.
func applyCachedDescriptor(manifest *Manifest, dir string) {
	wasmBytes, err := os.ReadFile(filepath.Join(dir, manifest.Backend.Binary)) //nolint:gosec // path constructed from trusted extension directory
	if err != nil {
		logger.Default().Warn("read wasm binary for descriptor lookup",
			zap.String("extension", manifest.Name), zap.Error(err))
		return
	}
	desc := cachedDescriptor(manifest.Name, WasmHash(wasmBytes))
	if desc == nil {
		logger.Default().Warn("no cached descriptor for extension; load it once to populate the cache",
			zap.String("extension", manifest.Name))
		return
	}
	manifest.apply(desc)
}

func (m *Manager) installLock(name string) *sync.Mutex {
	m.installMu.Lock()
	defer m.installMu.Unlock()
	mu, ok := m.installLocks[name]
	if !ok {
		mu = &sync.Mutex{}
		m.installLocks[name] = mu
	}
	return mu
}

// pendingDescriptor is a describe() answer that has not been written to the cache
// yet. Writing waits until the extension is actually in place: a staged upgrade that
// is abandoned must not overwrite the entry the running version was described by.
// A zero value (cache hit) has nothing to write.
type pendingDescriptor struct {
	name    string
	hash    string
	payload []byte
}

func (p pendingDescriptor) store() {
	if p.payload == nil {
		return
	}
	storeDescriptor(p.name, p.hash, p.payload)
}

// describeInto fills the manifest's functional face from the guest.
//
// The guest is asked only when the cache has no answer for this exact wasm binary;
// a hit skips the call entirely, which is what keeps listing extensions off the
// WASM path. The answer is validated either way — a cached payload crosses the same
// boundary as a fresh one. A fresh answer is returned for the caller to cache once
// the extension is committed.
func (m *Manager) describeInto(ctx context.Context, manifest *Manifest, plugin *Plugin, wasmBytes []byte) (pendingDescriptor, error) {
	hash := WasmHash(wasmBytes)
	if desc := cachedDescriptor(manifest.Name, hash); desc != nil {
		manifest.apply(desc)
		return pendingDescriptor{}, nil
	}
	payload, err := plugin.Describe(ctx)
	if err != nil {
		return pendingDescriptor{}, fmt.Errorf("describe extension %q: %w", manifest.Name, err)
	}
	desc, err := ParseDescriptor(payload)
	if err != nil {
		return pendingDescriptor{}, fmt.Errorf("extension %q: %w", manifest.Name, err)
	}
	manifest.apply(desc)
	return pendingDescriptor{name: manifest.Name, hash: hash, payload: payload}, nil
}

func (m *Manager) LoadExtension(ctx context.Context, dir string) (*Manifest, error) {
	ext, pending, err := m.loadExtension(ctx, dir, dir)
	if err != nil {
		return nil, err
	}
	pending.store()

	m.mu.Lock()
	m.extensions[ext.Name] = ext
	m.mu.Unlock()

	m.logger.Info("loaded extension", zap.String("name", ext.Name), zap.String("version", ext.Manifest.Version))
	return ext.Manifest, nil
}

// loadExtension reads, compiles and describes the extension whose files are in
// srcDir without publishing it anywhere. extDir is where the extension lives once in
// place — the capability sandbox and Extension.Dir are bound to it, so a version
// loaded from a staging directory is already correct after it is moved there.
func (m *Manager) loadExtension(ctx context.Context, srcDir, extDir string) (*Extension, pendingDescriptor, error) {
	manifestPath := filepath.Join(srcDir, "manifest.json")
	data, err := os.ReadFile(manifestPath) //nolint:gosec // extension directories are trusted
	if err != nil {
		return nil, pendingDescriptor{}, fmt.Errorf("read manifest: %w", err)
	}

	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, pendingDescriptor{}, err
	}

	wasmPath := filepath.Join(srcDir, manifest.Backend.Binary)
	wasmBytes, err := os.ReadFile(wasmPath) //nolint:gosec // path constructed from trusted extension directory
	if err != nil {
		return nil, pendingDescriptor{}, fmt.Errorf("read wasm binary: %w", err)
	}

	skillMD, skillDescription, err := readSkillMD(srcDir, manifest.Name, m.logger)
	if err != nil {
		return nil, pendingDescriptor{}, err
	}

	locales, err := LoadLocales(srcDir)
	if err != nil {
		return nil, pendingDescriptor{}, err
	}

	host := m.newHost(manifest.Name)
	host = NewCapabilityHost(host, manifest, extDir) // enforce capabilities declared in manifest
	plugin, err := LoadPlugin(ctx, manifest, wasmBytes, host, m.wasmCache)
	if err != nil {
		return nil, pendingDescriptor{}, fmt.Errorf("load plugin: %w", err)
	}

	pending, err := m.describeInto(ctx, manifest, plugin, wasmBytes)
	if err != nil {
		if closeErr := plugin.Close(ctx); closeErr != nil {
			m.logger.Warn("close plugin after describe failure", zap.String("name", manifest.Name), zap.Error(closeErr))
		}
		return nil, pendingDescriptor{}, err
	}

	return &Extension{
		Name:             manifest.Name,
		Dir:              extDir,
		Manifest:         manifest,
		Plugin:           plugin,
		SkillMD:          skillMD,
		SkillDescription: skillDescription,
		Locales:          locales,
	}, pending, nil
}

// ManifestInfo holds everything a host can learn about an extension without running
// it: the manifest's security contract, the cached describe() answer merged onto it,
// the shipped documentation and the locale tables. It is what a process with no WASM
// runtime at all (opsctl) has to work from.
type ManifestInfo struct {
	Name             string
	Dir              string
	Manifest         *Manifest
	Locales          map[string]map[string]string
	SkillMD          string // Body of SKILL.md (frontmatter stripped)
	SkillDescription string // description from SKILL.md frontmatter, if any
}

// ScanManifests reads every extension directory without loading WASM plugins.
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
		if !isExtensionDir(entry) {
			continue
		}
		extDir := filepath.Join(m.dir, entry.Name())
		manifestPath := filepath.Join(extDir, "manifest.json")
		data, err := os.ReadFile(manifestPath) //nolint:gosec // path constructed from ReadDir within extensions directory
		if err != nil {
			continue
		}
		manifest, err := ParseManifest(data)
		if err != nil {
			m.logger.Warn("skip extension manifest", zap.String("dir", entry.Name()), zap.Error(err))
			continue
		}
		// A loaded extension already holds the answer the guest gave this run; only
		// a disabled one has to fall back to what the cache remembers.
		if ext := m.GetExtension(manifest.Name); ext != nil {
			manifest = ext.Manifest
		} else {
			applyCachedDescriptor(manifest, extDir)
		}
		result = append(result, newManifestInfo(manifest, extDir))
	}
	return result, nil
}

// stagingPrefix names the directories Stage builds new versions in. They live inside
// the extensions directory so the final move is a same-filesystem rename, and the
// leading dot keeps them out of every scan.
const stagingPrefix = ".install-"

// isExtensionDir reports whether a directory entry of the extensions directory can
// hold an installed extension — dot-directories are the compilation cache and
// install staging areas.
func isExtensionDir(entry os.DirEntry) bool {
	return entry.IsDir() && !strings.HasPrefix(entry.Name(), ".")
}

// StagedInstall is a new version of an extension, loaded and described next to the
// installed one but not yet in its place. Nothing the running version depends on —
// its directory, its loaded module, its cached descriptor — has been touched: Commit
// swaps the new version in, Abort throws it away. Exactly one of them must be called.
type StagedInstall struct {
	m        *Manager
	root     string // staging area; removed by Commit and Abort
	newDir   string // the new version's files, inside root
	destDir  string
	ext      *Extension
	pending  pendingDescriptor
	finished bool
}

// Extension returns the staged version. Its Dir is already the final location.
func (s *StagedInstall) Extension() *Extension { return s.ext }

// Manifest returns the staged version's manifest, functional face included.
func (s *StagedInstall) Manifest() *Manifest { return s.ext.Manifest }

// Stage unpacks sourcePath (a directory or a .zip) into a staging area inside the
// extensions directory and loads it there. On failure the staging area is gone and
// the installed version, if any, is exactly as it was.
func (m *Manager) Stage(ctx context.Context, sourcePath string) (*StagedInstall, error) {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return nil, fmt.Errorf("create extensions dir: %w", err)
	}
	root, err := os.MkdirTemp(m.dir, stagingPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}
	staged, err := m.stageInto(ctx, root, sourcePath)
	if err != nil {
		removeStagingDir(root)
		return nil, err
	}
	return staged, nil
}

func (m *Manager) stageInto(ctx context.Context, root, sourcePath string) (*StagedInstall, error) {
	newDir := filepath.Join(root, "new")
	if strings.HasSuffix(strings.ToLower(sourcePath), ".zip") {
		if err := os.MkdirAll(newDir, 0755); err != nil {
			return nil, fmt.Errorf("create staging dir: %w", err)
		}
		if err := extractZip(sourcePath, newDir); err != nil {
			return nil, fmt.Errorf("extract zip: %w", err)
		}
	} else if err := copyDir(sourcePath, newDir); err != nil {
		return nil, fmt.Errorf("copy extension: %w", err)
	}

	// The name decides the final location, so it has to be known before loading.
	data, err := os.ReadFile(filepath.Join(newDir, "manifest.json")) //nolint:gosec // path inside our own staging dir
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	destDir := filepath.Join(m.dir, manifest.Name)

	ext, pending, err := m.loadExtension(ctx, newDir, destDir)
	if err != nil {
		return nil, fmt.Errorf("load extension: %w", err)
	}
	return &StagedInstall{m: m, root: root, newDir: newDir, destDir: destDir, ext: ext, pending: pending}, nil
}

// Commit moves the staged version into place and makes it the loaded one; the
// previous version's module is closed and its files removed. If the move fails the
// previous version is left in place and still loaded, and the staged one is
// discarded.
func (s *StagedInstall) Commit(ctx context.Context) error {
	if s.finished {
		return fmt.Errorf("staged install of %q already finished", s.ext.Name)
	}
	s.finished = true
	m := s.m
	name := s.ext.Name
	defer removeStagingDir(s.root)

	lock := m.installLock(name)
	lock.Lock()
	defer lock.Unlock()

	backup := filepath.Join(s.root, "old")
	hadOld := true
	if err := os.Rename(s.destDir, backup); err != nil {
		if !os.IsNotExist(err) {
			s.closeStaged(ctx)
			return fmt.Errorf("move installed version aside: %w", err)
		}
		hadOld = false
	}
	if err := os.Rename(s.newDir, s.destDir); err != nil {
		if hadOld {
			if restoreErr := os.Rename(backup, s.destDir); restoreErr != nil {
				m.logger.Error("restore installed extension after failed swap",
					zap.String("name", name), zap.Error(restoreErr))
			}
		}
		s.closeStaged(ctx)
		return fmt.Errorf("move new version into place: %w", err)
	}

	m.mu.Lock()
	old := m.extensions[name]
	m.extensions[name] = s.ext
	m.mu.Unlock()
	if old != nil {
		if err := old.Plugin.Close(ctx); err != nil {
			m.logger.Warn("close replaced extension plugin", zap.String("name", name), zap.Error(err))
		}
	}
	s.pending.store()

	m.logger.Info("installed extension", zap.String("name", name), zap.String("version", s.ext.Manifest.Version))
	return nil
}

// Abort discards the staged version. The installed version is untouched.
func (s *StagedInstall) Abort(ctx context.Context) {
	if s.finished {
		return
	}
	s.finished = true
	s.closeStaged(ctx)
	removeStagingDir(s.root)
}

func (s *StagedInstall) closeStaged(ctx context.Context) {
	if err := s.ext.Plugin.Close(ctx); err != nil {
		s.m.logger.Warn("close staged extension plugin", zap.String("name", s.ext.Name), zap.Error(err))
	}
}

func removeStagingDir(root string) {
	if err := os.RemoveAll(root); err != nil {
		logger.Default().Warn("remove extension staging dir", zap.String("dir", root), zap.Error(err))
	}
}

// Install installs an extension from a zip file or directory, replacing any
// installed version only once the new one has loaded.
func (m *Manager) Install(ctx context.Context, sourcePath string) (*Manifest, error) {
	staged, err := m.Stage(ctx, sourcePath)
	if err != nil {
		return nil, err
	}
	if err := staged.Commit(ctx); err != nil {
		return nil, err
	}
	return staged.Manifest(), nil
}

// Uninstall stops and removes an extension from disk.
func (m *Manager) Uninstall(ctx context.Context, name string) error {
	// Unload if loaded (ignore error if not loaded)
	_ = m.Unload(ctx, name)

	// The cached descriptor describes files that are about to stop existing.
	deleteDescriptor(name)

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
