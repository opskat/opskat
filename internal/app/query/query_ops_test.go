package query

import (
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestPanelDBCacheKey(t *testing.T) {
	t.Run("SQLite reuses one panel connection across schema names", func(t *testing.T) {
		cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Database: "main"}
		if got := panelDBCacheKey(7, cfg); got != "7" {
			t.Fatalf("panelDBCacheKey() = %q, want %q", got, "7")
		}
		cfg.Database = ""
		if got := panelDBCacheKey(7, cfg); got != "7" {
			t.Fatalf("panelDBCacheKey() = %q, want %q", got, "7")
		}
	})

	t.Run("network databases keep database in the cache key", func(t *testing.T) {
		cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverMySQL, Database: "app"}
		if got := panelDBCacheKey(7, cfg); got != "7:app" {
			t.Fatalf("panelDBCacheKey() = %q, want %q", got, "7:app")
		}
	})
}

func TestShouldCachePanelDB(t *testing.T) {
	t.Run("remote SQLite VFS is one-shot so the lock file is released after each operation", func(t *testing.T) {
		cfg := &asset_entity.DatabaseConfig{
			Driver:       asset_entity.DriverSQLite,
			SQLiteSource: asset_entity.SQLiteSourceRemoteSSHVFS,
		}
		if shouldCachePanelDB(cfg) {
			t.Fatal("remote SQLite VFS connection should not be cached")
		}
	})

	t.Run("local SQLite and network databases keep the existing panel cache behavior", func(t *testing.T) {
		if !shouldCachePanelDB(&asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite}) {
			t.Fatal("local SQLite connection should be cached")
		}
		if !shouldCachePanelDB(&asset_entity.DatabaseConfig{Driver: asset_entity.DriverMySQL}) {
			t.Fatal("network database connection should be cached")
		}
	})
}

func TestFinishPanelDBOperation(t *testing.T) {
	t.Run("returns cleanup error when operation succeeds", func(t *testing.T) {
		cleanupErr := errors.New("remove lock failed")
		err := finishPanelDBOperation(nil, func() error { return cleanupErr })
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want cleanup error", err)
		}
	})

	t.Run("keeps both operation and cleanup errors", func(t *testing.T) {
		opErr := errors.New("query failed")
		cleanupErr := errors.New("remove lock failed")
		err := finishPanelDBOperation(opErr, func() error { return cleanupErr })
		if !errors.Is(err, opErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want operation error", err)
		}
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want cleanup error", err)
		}
	})

	t.Run("returns operation error when cleanup succeeds", func(t *testing.T) {
		opErr := errors.New("query failed")
		err := finishPanelDBOperation(opErr, func() error { return nil })
		if !errors.Is(err, opErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want operation error", err)
		}
	})
}
