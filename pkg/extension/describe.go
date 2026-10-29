package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// DescribeCache persists describe() results so the answers survive a restart.
//
// Reading an extension's functional face means running its WASM module, and
// compiling a Go-built module costs seconds. Listing installed extensions, showing
// a disabled one's details, or resolving which extension owns an asset type from
// another process (opsctl) must not pay that: they read the cached answer instead.
// The wasm binary's hash is the key — a rebuilt module is a different extension as
// far as its declarations are concerned, so the entry is simply not used.
//
// The implementation lives behind the repository layer (extension_describe_repo)
// and is installed once at bootstrap; without it everything still works, extensions
// are just described on every load.
type DescribeCache interface {
	LoadDescriptor(name string) (wasmHash string, payload []byte, err error)
	StoreDescriptor(name, wasmHash string, payload []byte) error
	DeleteDescriptor(name string) error
}

var (
	describeCacheMu sync.RWMutex
	describeCache   DescribeCache
)

// SetDescribeCache installs the process-wide descriptor cache. Pass nil to detach.
func SetDescribeCache(c DescribeCache) {
	describeCacheMu.Lock()
	defer describeCacheMu.Unlock()
	describeCache = c
}

func currentDescribeCache() DescribeCache {
	describeCacheMu.RLock()
	defer describeCacheMu.RUnlock()
	return describeCache
}

// WasmHash identifies a wasm binary for descriptor caching.
func WasmHash(wasmBytes []byte) string {
	sum := sha256.Sum256(wasmBytes)
	return hex.EncodeToString(sum[:])
}

// PackageSHA256 是已安装扩展的包摘要：WASM 与 manifest.json 一起哈希。
// manifest 带着能力声明（网络白名单、tunnel、commandReview），只哈希 WASM 挡不住改能力后沿用旧摘要。
// 加载审核器前重算并与安装时记下的值比对；已加载的扩展用 Extension.PackageSHA256。
func PackageSHA256(dir, binary string) (string, error) {
	wasm, err := os.ReadFile(filepath.Join(dir, binary)) //nolint:gosec // 路径来自已安装扩展目录
	if err != nil {
		return "", fmt.Errorf("read wasm for package digest: %w", err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // 路径来自已安装扩展目录
	if err != nil {
		return "", fmt.Errorf("read manifest for package digest: %w", err)
	}
	return packageDigest(wasm, manifest), nil
}

func packageDigest(wasm, manifest []byte) string {
	h := sha256.New()
	_, _ = h.Write(wasm)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(manifest)
	return hex.EncodeToString(h.Sum(nil))
}

// cachedDescriptor returns the stored descriptor for name when it was produced by
// exactly this wasm binary, or nil.
func cachedDescriptor(name, wasmHash string) *Descriptor {
	cache := currentDescribeCache()
	if cache == nil {
		return nil
	}
	storedHash, payload, err := cache.LoadDescriptor(name)
	if err != nil {
		logger.Default().Warn("read cached extension descriptor",
			zap.String("extension", name), zap.Error(err))
		return nil
	}
	if len(payload) == 0 || storedHash != wasmHash {
		return nil
	}
	desc, err := ParseDescriptor(payload)
	if err != nil {
		// A cache entry that no longer parses is stale, not fatal: describing the
		// guest again produces the truth and overwrites it.
		logger.Default().Warn("discard unusable cached extension descriptor",
			zap.String("extension", name), zap.Error(err))
		return nil
	}
	return desc
}

func deleteDescriptor(name string) {
	cache := currentDescribeCache()
	if cache == nil {
		return
	}
	if err := cache.DeleteDescriptor(name); err != nil {
		logger.Default().Warn("drop cached extension descriptor",
			zap.String("extension", name), zap.Error(err))
	}
}

func storeDescriptor(name, wasmHash string, payload []byte) {
	cache := currentDescribeCache()
	if cache == nil {
		return
	}
	if err := cache.StoreDescriptor(name, wasmHash, payload); err != nil {
		logger.Default().Warn("cache extension descriptor",
			zap.String("extension", name), zap.Error(err))
	}
}
