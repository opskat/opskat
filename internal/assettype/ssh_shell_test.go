package assettype

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/policy"
)

// 新建 SSH 资产（以及「恢复默认」）时的默认命令策略按远端 shell 选：PowerShell 资产挂 Windows 组，
// 其余挂 Linux 组。
func TestSSHDefaultPolicyFollowsRemoteShell(t *testing.T) {
	ctx := context.Background()
	for config, want := range map[string][]string{
		`{"host":"h","port":22,"username":"u","auth_type":"password","shell":"powershell"}`: {policy.BuiltinWindowsReadOnly, policy.BuiltinWindowsDangerousDeny},
		`{"host":"h","port":22,"username":"u","auth_type":"password"}`:                      {policy.BuiltinLinuxReadOnly, policy.BuiltinDangerousDeny},
		`{"host":"h","port":22,"username":"u","auth_type":"password","shell":"posix"}`:      {policy.BuiltinLinuxReadOnly, policy.BuiltinDangerousDeny},
	} {
		p, ok, err := policy.DefaultPolicyForAsset(ctx, "ssh", config)
		require.NoError(t, err, config)
		require.True(t, ok, config)
		require.Equal(t, want, p.(*policy.CommandPolicy).Groups, config)
	}
}
