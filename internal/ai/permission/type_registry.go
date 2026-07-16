package permission

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

type permissionCheckFunc func(context.Context, int64, string) aictx.CheckResult

// permissionTypeHandler is the single source of truth for permission dispatch,
// opsctl aliases, approval item types, and grant normalization behavior.
type permissionTypeHandler struct {
	canonical    string
	approvalType string
	shellLike    bool
	check        permissionCheckFunc
}

var permissionTypes = make(map[string]*permissionTypeHandler)

func registerPermissionType(canonical, approvalType string, shellLike bool, check permissionCheckFunc, aliases ...string) {
	if canonical == "" || approvalType == "" || check == nil {
		panic("permission: invalid type registration")
	}
	handler := &permissionTypeHandler{
		canonical:    canonical,
		approvalType: approvalType,
		shellLike:    shellLike,
		check:        check,
	}
	for _, name := range append([]string{canonical}, aliases...) {
		if _, exists := permissionTypes[name]; exists {
			panic(fmt.Sprintf("permission: duplicate type registration %q", name))
		}
		permissionTypes[name] = handler
	}
}

func permissionTypeFor(name string) (*permissionTypeHandler, bool) {
	handler, ok := permissionTypes[name]
	return handler, ok
}

func init() {
	registerPermissionType(asset_entity.AssetTypeSSH, "exec", true, checkCommandPolicyPermission, "exec")
	registerPermissionType(asset_entity.AssetTypeSerial, "serial", false, checkCommandPolicyPermission)
	registerPermissionType(asset_entity.AssetTypeDatabase, "sql", false, checkDatabasePermission, "sql")
	registerPermissionType(asset_entity.AssetTypeRedis, "redis", false, checkRedisPermission)
	registerPermissionType(asset_entity.AssetTypeEtcd, "etcd", false, checkEtcdPermission)
	registerPermissionType(asset_entity.AssetTypeMongoDB, "mongo", false, checkMongoDBPermission, "mongo")
	registerPermissionType(asset_entity.AssetTypeKafka, "kafka", false, checkKafkaPermission)
	registerPermissionType(asset_entity.AssetTypeK8s, "k8s", true, checkK8sPermission)
}
