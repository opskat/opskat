package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/fsnotify/fsnotify"
	"github.com/opskat/opskat/pkg/skillmd"
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
func LoadLocales(dir string) map[string]map[string]string {
	localesDir := filepath.Join(dir, "locales")
	entries, err := os.ReadDir(localesDir)
	if err != nil {
		return nil
	}
	result := make(map[string]map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		lang := strings.ToLower(strings.TrimSuffix(entry.Name(), ".json"))
		data, err := os.ReadFile(filepath.Join(localesDir, entry.Name())) //nolint:gosec // path constructed from ReadDir within known locales directory
		if err != nil {
			continue
		}
		var m map[string]string
		if json.Unmarshal(data, &m) == nil {
			result[lang] = m
		}
	}
	return result
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

// Watch monitors the extensions directory for changes and calls onChange.
// The caller is responsible for handling the reload logic.
func (m *Manager) Watch(ctx context.Context, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}

	if err := os.MkdirAll(m.dir, 0755); err != nil {
		if closeErr := watcher.Close(); closeErr != nil {
			logger.Default().Warn("close watcher after mkdir error", zap.Error(closeErr))
		}
		return fmt.Errorf("create extensions dir: %w", err)
	}

	if err := watcher.Add(m.dir); err != nil {
		if closeErr := watcher.Close(); closeErr != nil {
			logger.Default().Warn("close watcher after add error", zap.Error(closeErr))
		}
		return fmt.Errorf("watch extensions dir: %w", err)
	}

	go func() {
		defer func() {
			if err := watcher.Close(); err != nil {
				logger.Default().Warn("close filesystem watcher", zap.Error(err))
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Write|fsnotify.Rename) != 0 {
					m.logger.Info("extension directory changed",
						zap.String("file", event.Name),
						zap.String("op", event.Op.String()))
					if onChange != nil {
						onChange()
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

// LoadManifestInfo reads a manifest from disk without loading the WASM plugin.
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
	return &ManifestInfo{Name: manifest.Name, Dir: dir, Manifest: manifest, Locales: LoadLocales(dir)}, nil
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

func (m *Manager) LoadExtension(ctx context.Context, dir string) (*Manifest, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath) //nolint:gosec // extension directories are trusted
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}

	wasmPath := filepath.Join(dir, manifest.Backend.Binary)
	wasmBytes, err := os.ReadFile(wasmPath) //nolint:gosec // path constructed from trusted extension directory
	if err != nil {
		return nil, fmt.Errorf("read wasm binary: %w", err)
	}

	// SKILL.md 可选；当它存在且带 frontmatter 时，frontmatter 必须合规。
	//
	// 此前这里有一个 4 KiB 硬上限，理由是整份正文会进系统提示词。上限是错的解法：
	// 它把"文档写长了"变成"整个扩展加载失败"，而真正该做的是解析 frontmatter、
	// 让 description 进清单、正文只在相关 Tab 打开时注入（bridge → chat.go → prompt_builder）。
	// 上限去掉，严格性移到格式上：frontmatter 解析失败响亮失败。
	//
	// 但"没有 frontmatter"本身不算格式错误：扩展 SKILL.md 早于 frontmatter 约定
	// 存在（已发布的 extensions/oss/SKILL.md 就是裸 Markdown，首行是 `# OSS ...`），
	// 我们不能反向修改另一个仓库来配合本仓的严格化。内置 skill（internal/ai/skills）
	// 是本仓完全控制的一手内容，缺 frontmatter 就该 panic；扩展是边界之外的第三方
	// 内容，边界处的规则是「宽进严出」——没有 frontmatter 就退化成整份原文当正文
	// （等价于此前的行为，只是去掉了 4 KiB 上限），真正写坏了 frontmatter（写了
	// 开头分隔符但没写全）才响亮失败。
	skillMD := ""
	skillDescription := ""
	if data, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err == nil { //nolint:gosec // path constructed from trusted extension directory
		raw := string(data)
		parsed, perr := skillmd.Parse(raw)
		switch {
		case perr == nil:
			skillMD = parsed.Body
			skillDescription = parsed.Description
		case errors.Is(perr, skillmd.ErrNoFrontmatter):
			skillMD = raw
		default:
			return nil, fmt.Errorf("SKILL.md: %w", perr)
		}
	}

	host := m.newHost(manifest.Name)
	host = NewCapabilityHost(host, manifest, dir) // enforce capabilities declared in manifest
	plugin, err := LoadPlugin(ctx, manifest, wasmBytes, host, m.wasmCache)
	if err != nil {
		host.CloseAll()
		return nil, fmt.Errorf("load plugin: %w", err)
	}

	ext := &Extension{
		Name:             manifest.Name,
		Dir:              dir,
		Manifest:         manifest,
		Plugin:           plugin,
		SkillMD:          skillMD,
		SkillDescription: skillDescription,
		Locales:          LoadLocales(dir),
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
	Locales  map[string]map[string]string
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
		data, err := os.ReadFile(manifestPath) //nolint:gosec // path constructed from ReadDir within extensions directory
		if err != nil {
			continue
		}
		manifest, err := ParseManifest(data)
		if err != nil {
			m.logger.Warn("skip extension manifest", zap.String("dir", entry.Name()), zap.Error(err))
			continue
		}
		result = append(result, &ManifestInfo{
			Name:     manifest.Name,
			Dir:      extDir,
			Manifest: manifest,
			Locales:  LoadLocales(extDir),
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
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				logger.Default().Warn("remove temp dir", zap.String("dir", tmpDir), zap.Error(err))
			}
		}()
		if err := extractZip(sourcePath, tmpDir); err != nil {
			return nil, fmt.Errorf("extract zip: %w", err)
		}
		sourceDir = tmpDir
	}

	// Read and validate manifest
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	data, err := os.ReadFile(manifestPath) //nolint:gosec // path constructed from validated source directory
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}

	lock := m.installLock(manifest.Name)
	lock.Lock()
	defer lock.Unlock()

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
		if removeErr := os.RemoveAll(destDir); removeErr != nil {
			logger.Default().Warn("remove extension dir after load failure", zap.String("dir", destDir), zap.Error(removeErr))
		}
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
