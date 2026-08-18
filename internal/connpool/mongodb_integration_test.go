//go:build integration

package connpool

import (
	"context"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// Run: go test -tags integration ./internal/connpool -run TestDialMongoDBLegacyIntegration -v
func TestDialMongoDBLegacyIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("v2 against MongoDB 7 on 27018", func(t *testing.T) {
		cfg := &asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27018}
		closer, tunnel, err := DialMongoDB(ctx, &asset_entity.Asset{}, cfg, "", nil)
		if err != nil {
			t.Fatalf("DialMongoDB v2: %v", err)
		}
		defer closer.Close()
		if tunnel != nil {
			defer tunnel.Close()
		}
		if closer.Legacy || closer.V2 == nil {
			t.Fatalf("expected v2 client, got Legacy=%v V2=%v", closer.Legacy, closer.V2)
		}
	})

	t.Run("v1 legacy against MongoDB 3.6 on 27017", func(t *testing.T) {
		cfg := &asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27017, LegacyCompat: true}
		closer, tunnel, err := DialMongoDB(ctx, &asset_entity.Asset{}, cfg, "", nil)
		if err != nil {
			t.Fatalf("DialMongoDB v1 legacy: %v", err)
		}
		defer closer.Close()
		if tunnel != nil {
			defer tunnel.Close()
		}
		if !closer.Legacy || closer.V1 == nil {
			t.Fatalf("expected v1 client, got Legacy=%v V1=%v", closer.Legacy, closer.V1)
		}
	})

	t.Run("v2 fails against MongoDB 3.6 on 27017", func(t *testing.T) {
		cfg := &asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27017, LegacyCompat: false}
		_, _, err := DialMongoDB(ctx, &asset_entity.Asset{}, cfg, "", nil)
		if err == nil {
			t.Fatal("expected v2 dial to fail against MongoDB 3.6, got nil error")
		}
	})
}
