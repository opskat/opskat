package command

import (
	"testing"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/stretchr/testify/require"
)

func TestApplyEnvironmentOverridesUsesDataDirectory(t *testing.T) {
	wantDataDir := t.TempDir()
	t.Setenv("OPSKAT_DATA_DIR", wantDataDir)
	t.Setenv("OPSKAT_MASTER_KEY", "env-master-key")

	dataDir, masterKey := applyEnvironmentOverrides("", "")

	require.Equal(t, wantDataDir, dataDir)
	require.Equal(t, "env-master-key", masterKey)
}

func TestTopLevelUsageNamesCredentialReadCommands(t *testing.T) {
	usage := captureStderr(t, printUsage)
	require.Contains(t, usage, "opsctl list credentials")
	require.Contains(t, usage, "opsctl get credential")
}

// session 已降为内部概念：使用说明不得再宣传已删除的 --session 全局 flag
// 与 session 子命令。
func TestTopLevelUsageRetiresSessionSurface(t *testing.T) {
	usage := captureStderr(t, printUsage)
	require.NotContains(t, usage, "--session")
	require.NotContains(t, usage, "opsctl session")
}

// grant submit 已删除（能力由 policy allow/deny 承接）：使用说明不得再宣传
// grant 命令，老脚本会以未知命令失败而非静默换语义，这是刻意的。
func TestTopLevelUsageRetiresGrantSurface(t *testing.T) {
	usage := captureStderr(t, printUsage)
	require.NotContains(t, usage, "grant")
}

func TestCreateAssetUsageDocumentsGenericAndSafeCredentialInputs(t *testing.T) {
	usage := captureStderr(t, printCreateAssetUsage)
	for _, want := range []string{
		"--config", "--config-file", "opsctl help <type>", "--kubeconfig-file",
		"--credential-id", "--password-stdin", "--password",
		"--agent-source-id", "--agent-key-fingerprint", "shell history", "process listings", "CI",
		"restrictive file permissions", "avoid committing", "remove it",
	} {
		require.Contains(t, usage, want)
	}
	for _, handler := range assettype.All() {
		require.Contains(t, usage, handler.Type())
	}
}

func TestApplyEnvironmentOverridesKeepsExplicitFlags(t *testing.T) {
	t.Setenv("OPSKAT_DATA_DIR", "env-data")
	t.Setenv("OPSKAT_MASTER_KEY", "env-key")

	dataDir, masterKey := applyEnvironmentOverrides("flag-data", "flag-key")

	require.Equal(t, "flag-data", dataDir)
	require.Equal(t, "flag-key", masterKey)
}
