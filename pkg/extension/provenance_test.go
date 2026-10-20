package extension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageSHA256(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm-bytes"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"x"}`), 0o600))

	sum, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)
	again, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)
	assert.Equal(t, sum, again)
	assert.Len(t, sum, 64)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm-bytes-changed"), 0o600))
	changed, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)
	assert.NotEqual(t, sum, changed, "改了 WASM 必须得到不同摘要")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm-bytes"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"x","capabilities":{"commandReview":true}}`), 0o600))
	manifestChanged, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)
	assert.NotEqual(t, sum, manifestChanged, "改了 manifest 的能力声明必须得到不同摘要")

	_, err = PackageSHA256(dir, "missing.wasm")
	require.Error(t, err)
}
