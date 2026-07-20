package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestInit_RegistersFiveVerbatimTypes(t *testing.T) {
	want := []string{
		asset_entity.AssetTypeSSH,
		asset_entity.AssetTypeSerial,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
		asset_entity.AssetTypeK8s,
	}
	for _, at := range want {
		if _, ok := permission.ExecutorFor(at); !ok {
			t.Fatalf("no executor registered for %q", at)
		}
	}
}

func TestInit_HelpDocAttachedForEachType(t *testing.T) {
	for _, at := range []string{
		asset_entity.AssetTypeSSH,
		asset_entity.AssetTypeSerial,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
		asset_entity.AssetTypeK8s,
	} {
		help, ok := permission.HelpFor(at)
		if !ok || help == "" {
			t.Fatalf("no help doc registered for %q", at)
		}
	}
}

func TestInit_StructuredTypesNotYetRegistered(t *testing.T) {
	// Plan B 才补 mongo / etcd / kafka；此处锁定 Plan A 的边界。
	for _, at := range []string{
		asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeEtcd,
		asset_entity.AssetTypeKafka,
	} {
		if _, ok := permission.ExecutorFor(at); ok {
			t.Fatalf("%q should not be registered in Plan A", at)
		}
	}
}
