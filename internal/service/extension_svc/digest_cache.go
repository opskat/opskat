package extension_svc

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// packageDigests caches PackageSHA256 per extension. Command review asks for the
// install facts on every verdict and the package is 10+ MB, so the digest is only
// recomputed when a package file's size or mtime differs from what it was computed
// from; the lifecycle calls (install, reload, uninstall) also drop it explicitly.
type packageDigests struct {
	// sum computes the digest of a package directory; PackageSHA256 outside tests.
	sum func(dir string) (string, error)

	mu      sync.Mutex
	entries map[string]digestEntry
}

type digestEntry struct {
	dir         string
	fingerprint []fileStamp
	sum         string
}

// fileStamp is what a package file looked like when it was hashed.
type fileStamp struct {
	rel     string
	size    int64
	modTime time.Time
}

func newPackageDigests() *packageDigests {
	return &packageDigests{sum: PackageSHA256, entries: map[string]digestEntry{}}
}

// get returns the digest of the package of extension name in dir.
func (c *packageDigests) get(name, dir string) (string, error) {
	stamp, err := stampPackage(dir)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok && e.dir == dir && sameStamps(e.fingerprint, stamp) {
		return e.sum, nil
	}
	sum, err := c.sum(dir)
	if err != nil {
		return "", err
	}
	c.entries[name] = digestEntry{dir: dir, fingerprint: stamp, sum: sum}
	return sum, nil
}

// invalidate drops the cached digest of name.
func (c *packageDigests) invalidate(name string) {
	c.mu.Lock()
	delete(c.entries, name)
	c.mu.Unlock()
}

// invalidateAll drops every cached digest.
func (c *packageDigests) invalidateAll() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

// stampPackage lists the package files with size and mtime, sorted by path. Like
// PackageSHA256 it refuses anything that is not a regular file.
func stampPackage(dir string) ([]fileStamp, error) {
	var stamps []fileStamp
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("package file %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		stamps = append(stamps, fileStamp{rel: filepath.ToSlash(rel), size: info.Size(), modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("package digest: %w", err)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].rel < stamps[j].rel })
	return stamps, nil
}

func sameStamps(a, b []fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].rel != b[i].rel || a[i].size != b[i].size || !a[i].modTime.Equal(b[i].modTime) {
			return false
		}
	}
	return true
}
