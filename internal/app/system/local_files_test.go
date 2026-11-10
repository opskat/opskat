package system

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatLocalPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, os.Chtimes(file, modTime, modTime))
	missing := filepath.Join(dir, "missing.txt")

	stats, err := (&System{}).StatLocalPaths([]string{file, dir, missing, "relative/path", ""})
	require.NoError(t, err)
	require.Len(t, stats, 5)

	assert.Equal(t, LocalPathStat{Path: file, Size: 5, ModTime: modTime.UnixMilli()}, stats[0])

	assert.Equal(t, dir, stats[1].Path)
	assert.True(t, stats[1].IsDir)
	assert.Empty(t, stats[1].Error)

	// 取不到的项逐个带原因返回，不拖累其余项。
	assert.Equal(t, missing, stats[2].Path)
	assert.NotEmpty(t, stats[2].Error)
	assert.NotEmpty(t, stats[3].Error, "relative paths are rejected at the boundary")
	assert.NotEmpty(t, stats[4].Error, "empty paths are rejected at the boundary")
}
