package query

import (
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
