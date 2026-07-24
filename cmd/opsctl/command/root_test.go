package command

import (
	"testing"

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

func TestApplyEnvironmentOverridesKeepsExplicitFlags(t *testing.T) {
	t.Setenv("OPSKAT_DATA_DIR", "env-data")
	t.Setenv("OPSKAT_MASTER_KEY", "env-key")

	dataDir, masterKey := applyEnvironmentOverrides("flag-data", "flag-key")

	require.Equal(t, "flag-data", dataDir)
	require.Equal(t, "flag-key", masterKey)
}
