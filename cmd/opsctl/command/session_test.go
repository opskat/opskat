package command

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubSessionDataDir 把 session 存储的数据目录解析指到临时目录，避免测试触碰
// 真实平台数据目录（与 bootstrap.portableDir 同款变量注入缝）。
func stubSessionDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := sessionDataDir
	sessionDataDir = func() string { return dir }
	t.Cleanup(func() { sessionDataDir = orig })
	return dir
}

// soleFileIn 断言目录里恰好剩一个普通文件并返回其路径——session 在 data dir 里
// 是单例文件，不允许再出现 sessions/<scope> 子树。
func soleFileIn(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected exactly one session file in %s", dir)
	require.False(t, entries[0].IsDir(), "session store must be a single file, not a directory tree")
	return filepath.Join(dir, entries[0].Name())
}

func TestResolveSessionIDIsDataDirSingleton(t *testing.T) {
	dataDir := stubSessionDataDir(t)
	projectDir := t.TempDir()
	t.Chdir(projectDir)

	id := "11111111-2222-3333-4444-555555555555"
	require.NoError(t, writeActiveSession(id))

	// session 跟随 data dir 而非 CWD：换目录仍读到同一会话。
	t.Chdir(t.TempDir())
	require.Equal(t, id, resolveSessionID())

	// data dir 根下只有一个文件——没有 sessions/<scope> 子树。
	soleFileIn(t, dataDir)

	// 终端环境变量不再派生 scope：另一个终端读到同一会话。
	t.Setenv("ITERM_SESSION_ID", "w0t0p0:BBBBBBBB")
	require.Equal(t, id, resolveSessionID())

	// 项目目录不被写入任何东西。
	entries, err := os.ReadDir(projectDir)
	require.NoError(t, err)
	require.Empty(t, entries, ".opskat/ must not be created in the working directory")
}

func TestResolveSessionIDExpiresByMtime(t *testing.T) {
	dataDir := stubSessionDataDir(t)
	id := "11111111-2222-3333-4444-555555555555"
	require.NoError(t, writeActiveSession(id))
	path := soleFileIn(t, dataDir)

	// 新写入的 session 有效。
	require.Equal(t, id, resolveSessionID())

	// 24 小时减一分钟：仍在有效期内（过期语义与旧实现一致）。
	almost := time.Now().Add(-sessionMaxAge + time.Minute)
	require.NoError(t, os.Chtimes(path, almost, almost))
	require.Equal(t, id, resolveSessionID())

	// 超过 24 小时：过期，且过期文件被移除。
	expired := time.Now().Add(-sessionMaxAge - time.Minute)
	require.NoError(t, os.Chtimes(path, expired, expired))
	require.Empty(t, resolveSessionID())
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err), "expired session file must be removed")
}

func TestResolveSessionIDIgnoresLegacyProjectSessionFiles(t *testing.T) {
	stubSessionDataDir(t)
	cwd := t.TempDir()
	legacyDir := filepath.Join(cwd, ".opskat", "sessions")
	require.NoError(t, os.MkdirAll(legacyDir, 0755))
	legacy := filepath.Join(legacyDir, "a1b2c3d4e5f6")
	require.NoError(t, os.WriteFile(legacy, []byte("99999999-8888-7777-6666-555555555555\n"), 0644))
	t.Chdir(cwd)

	// 旧 .opskat/sessions/<scope> 文件不被读取……
	require.Empty(t, resolveSessionID())

	// ……也不迁移、不删除，留给用户自行清理。
	data, err := os.ReadFile(legacy) //nolint:gosec // path is constructed from t.TempDir
	require.NoError(t, err)
	require.Equal(t, "99999999-8888-7777-6666-555555555555\n", string(data))
}

func TestResolveSessionIDRejectsShortContent(t *testing.T) {
	dataDir := stubSessionDataDir(t)
	require.NoError(t, writeActiveSession("11111111-2222-3333-4444-555555555555"))
	path := soleFileIn(t, dataDir)
	require.NoError(t, os.WriteFile(path, []byte("abc\n"), 0644))

	require.Empty(t, resolveSessionID())
}
