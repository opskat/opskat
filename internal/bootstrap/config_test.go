package bootstrap

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// UpdateConfig must serialize concurrent read-modify-write so no update is lost,
// and must not race with concurrent GetConfig reads. This is the root-cause fix
// for the background auto-backup timer goroutine racing with other config writers.
// runtime.Gosched widens the read-modify-write window; with a correct lock the
// total is still exact. (The memory race itself is caught by `go test -race`.)
func TestUpdateConfigConcurrentNoLostUpdates(t *testing.T) {
	configPath = filepath.Join(t.TempDir(), "config.json")
	appConfig = &AppConfig{}

	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = UpdateConfig(func(c *AppConfig) {
				v := c.WindowWidth
				runtime.Gosched()
				c.WindowWidth = v + 1
			})
			_ = GetConfig() // exercise reads concurrent with writes
		}()
	}
	wg.Wait()
	if got := GetConfig().WindowWidth; got != n {
		t.Fatalf("lost concurrent updates: got %d want %d", got, n)
	}
}

// GetConfig returns an isolated copy: mutating the returned struct (including its
// slice fields) must not leak into the stored config.
func TestGetConfigReturnsIsolatedCopy(t *testing.T) {
	configPath = filepath.Join(t.TempDir(), "config.json")
	appConfig = &AppConfig{
		WebDAVURL:                 "https://example.com/dav/",
		ExternalEditCustomEditors: []ExternalEditorConfig{{ID: "a", Args: []string{"--wait"}}},
	}

	snap := GetConfig()
	snap.WebDAVURL = "mutated"
	snap.ExternalEditCustomEditors[0].ID = "mutated"
	snap.ExternalEditCustomEditors[0].Args[0] = "mutated"
	snap.ExternalEditCustomEditors = append(snap.ExternalEditCustomEditors, ExternalEditorConfig{ID: "b"})

	cur := GetConfig()
	if cur.WebDAVURL != "https://example.com/dav/" {
		t.Fatalf("scalar field leaked: %q", cur.WebDAVURL)
	}
	if len(cur.ExternalEditCustomEditors) != 1 ||
		cur.ExternalEditCustomEditors[0].ID != "a" ||
		cur.ExternalEditCustomEditors[0].Args[0] != "--wait" {
		t.Fatalf("slice field aliased: %+v", cur.ExternalEditCustomEditors)
	}
}

// UpdateConfig persists changes so a subsequent GetConfig observes them, and
// reports an error when config was never loaded.
func TestUpdateConfig(t *testing.T) {
	configPath = filepath.Join(t.TempDir(), "config.json")
	appConfig = &AppConfig{}
	if err := UpdateConfig(func(c *AppConfig) { c.DebugMode = true }); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if !GetConfig().DebugMode {
		t.Fatalf("update not persisted")
	}

	appConfig = nil
	if err := UpdateConfig(func(c *AppConfig) { c.DebugMode = false }); err == nil {
		t.Fatalf("expected error when config not loaded")
	}
}
