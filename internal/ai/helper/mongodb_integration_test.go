//go:build integration

package helper

import (
	"context"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// Run: go test -tags integration ./internal/ai/helper -run TestMongoLegacyHelperIntegration -v
func TestMongoLegacyHelperIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dial := func(port int, legacy bool) *connpool.MongoClientCloser {
		t.Helper()
		cfg := &asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: port, LegacyCompat: legacy}
		closer, _, err := connpool.DialMongoDB(ctx, &asset_entity.Asset{}, cfg, "", nil)
		if err != nil {
			t.Fatalf("DialMongoDB legacy=%v port=%d: %v", legacy, port, err)
		}
		return closer
	}

	t.Run("v1 listDatabases on MongoDB 3.6", func(t *testing.T) {
		closer := dial(27017, true)
		defer closer.Close()
		out, err := ExecuteMongoDB(ctx, closer, "", "", "listDatabases", "{}")
		if err != nil {
			t.Fatalf("ExecuteMongoDB listDatabases: %v", err)
		}
		if out == "" || out == "{}" {
			t.Fatalf("unexpected empty result: %q", out)
		}
	})

	t.Run("v2 listDatabases on MongoDB 7", func(t *testing.T) {
		closer := dial(27018, false)
		defer closer.Close()
		out, err := ExecuteMongoDB(ctx, closer, "", "", "listDatabases", "{}")
		if err != nil {
			t.Fatalf("ExecuteMongoDB listDatabases: %v", err)
		}
		if out == "" || out == "{}" {
			t.Fatalf("unexpected empty result: %q", out)
		}
	})

	t.Run("v1 find on MongoDB 3.6", func(t *testing.T) {
		closer := dial(27017, true)
		defer closer.Close()
		_, err := ExecuteMongoDB(ctx, closer, "test", "items", "find", `{"filter":{}}`)
		if err != nil {
			t.Fatalf("ExecuteMongoDB find: %v", err)
		}
	})
}
