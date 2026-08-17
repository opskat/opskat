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
