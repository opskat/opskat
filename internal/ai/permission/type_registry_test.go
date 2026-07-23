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

// TestApprovalTypeFor locks the two branches of ApprovalTypeFor: registered types (and
// their protocol aliases) resolve to the handler's approvalType, exactly like
// permissionTypeFor; unregistered types fall back to the input unchanged — NOT to the
// literal "exec" that HandleConfirm used to hardcode as its zero value
// (internal/ai/permission/checker.go, Important 2 in the review this test backs).
//
// The unregistered-type case is not a hypothetical: extensions declare an arbitrary
// Policies.Type in their manifest (e.g. "oss", see pkg/extension/manifest_test.go) and
// tool_handler_ext.go's handleExecTool calls checker.HandleConfirm with it directly — that
// type is never going to be in permissionTypes. Falling back to "exec" would show an "OSS"
// approval as an "EXEC" badge in the front end (ApprovalBlock.tsx's TypeBadge), which is
// actively misleading about what is being approved; falling back to the type itself just
// shows an unstyled-but-honest "OSS" badge (TypeBadge defaults to a generic icon for
// unknown types, it does not error).
func TestApprovalTypeFor(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"ssh canonical", asset_entity.AssetTypeSSH, "exec"},
		{"ssh alias exec", "exec", "exec"},
		{"serial canonical", asset_entity.AssetTypeSerial, "serial"},
		{"database canonical", asset_entity.AssetTypeDatabase, "sql"},
		{"database alias sql", "sql", "sql"},
		{"redis canonical", asset_entity.AssetTypeRedis, "redis"},
		{"etcd canonical", asset_entity.AssetTypeEtcd, "etcd"},
		{"mongodb canonical", asset_entity.AssetTypeMongoDB, "mongo"},
		{"mongodb alias mongo", "mongo", "mongo"},
		{"kafka canonical", asset_entity.AssetTypeKafka, "kafka"},
		{"k8s canonical", asset_entity.AssetTypeK8s, "k8s"},
		{"cp grant tool", GrantToolCp, "cp"},
		{"unregistered type falls back to itself, not exec", "oss", "oss"},
		{"unregistered arbitrary string", "unknown-thing", "unknown-thing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ApprovalTypeFor(tt.input))
		})
	}
}

func TestSupportsGrantApprovalUsesPermissionRegistry(t *testing.T) {
	for _, approvalType := range []string{"exec", "serial", "sql", "redis", "etcd", "mongo", "kafka", "k8s", GrantToolCp} {
		assert.True(t, SupportsGrantApproval(approvalType), approvalType)
	}
	for _, approvalType := range []string{"create", "update", "delete", "ext_tool", "unknown"} {
		assert.False(t, SupportsGrantApproval(approvalType), approvalType)
	}
}
