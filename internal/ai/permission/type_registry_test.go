package permission

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPermissionTypeRegistry(t *testing.T) {
	tests := []struct {
		input        string
		canonical    string
		approvalType string
		shellLike    bool
	}{
		{asset_entity.AssetTypeSSH, asset_entity.AssetTypeSSH, "exec", true},
		{"exec", asset_entity.AssetTypeSSH, "exec", true},
		{asset_entity.AssetTypeSerial, asset_entity.AssetTypeSerial, "serial", false},
		{asset_entity.AssetTypeDatabase, asset_entity.AssetTypeDatabase, "sql", false},
		{"sql", asset_entity.AssetTypeDatabase, "sql", false},
		{asset_entity.AssetTypeRedis, asset_entity.AssetTypeRedis, "redis", false},
		{asset_entity.AssetTypeEtcd, asset_entity.AssetTypeEtcd, "etcd", false},
		{asset_entity.AssetTypeMongoDB, asset_entity.AssetTypeMongoDB, "mongo", false},
		{"mongo", asset_entity.AssetTypeMongoDB, "mongo", false},
		{asset_entity.AssetTypeKafka, asset_entity.AssetTypeKafka, "kafka", false},
		{asset_entity.AssetTypeK8s, asset_entity.AssetTypeK8s, "k8s", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			handler, ok := permissionTypeFor(tt.input)
			require.True(t, ok)
			assert.Equal(t, tt.canonical, handler.canonical)
			assert.Equal(t, tt.approvalType, handler.approvalType)
			assert.Equal(t, tt.shellLike, handler.shellLike)
			require.NotNil(t, handler.check)
		})
	}

	_, ok := permissionTypeFor("unknown")
	assert.False(t, ok)
}
