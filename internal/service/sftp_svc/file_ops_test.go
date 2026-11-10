package sftp_svc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
)

func TestStatTargetsReportsExistingEntriesWithKeepBothName(t *testing.T) {
	service, session, root := newTestService(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a_copy.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "logs"), 0o700))

	file := sftpiotest.RemotePath(filepath.Join(root, "a.txt"))
	missing := sftpiotest.RemotePath(filepath.Join(root, "new.txt"))
	dir := sftpiotest.RemotePath(filepath.Join(root, "logs"))

	got, err := service.StatTargets(session, []string{file, missing, dir})
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Equal(t, file, got[0].Path)
	assert.True(t, got[0].Exists)
	assert.False(t, got[0].IsDir)
	assert.Equal(t, int64(3), got[0].Size)
	assert.NotZero(t, got[0].ModTime)
	assert.Equal(t, sftpiotest.RemotePath(filepath.Join(root, "a_copy2.txt")), got[0].KeepBothPath,
		"保留两者沿用粘贴的 _copy 命名规则，并跳过已被占用的 _copy")
	assert.Empty(t, got[0].Error)

	assert.Equal(t, missing, got[1].Path)
	assert.False(t, got[1].Exists)
	assert.Empty(t, got[1].KeepBothPath)
	assert.Empty(t, got[1].Error)

	assert.True(t, got[2].Exists)
	assert.True(t, got[2].IsDir)
	assert.Equal(t, sftpiotest.RemotePath(filepath.Join(root, "logs_copy")), got[2].KeepBothPath)
}

func TestStatTargetsReportsCheckFailureInsteadOfMissing(t *testing.T) {
	service, session, root := newTestService(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o600))

	// 父路径是文件：服务端返回的是一般性失败而不是"不存在"，不能被当成可以直接写入。
	underFile := sftpiotest.RemotePath(filepath.Join(root, "a.txt", "child"))
	got, err := service.StatTargets(session, []string{underFile})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Exists)
	assert.NotEmpty(t, got[0].Error)
}

func pasteFixture(t *testing.T) (*Service, string, string) {
	t.Helper()
	service, session, root := newTestService(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(root, "dst"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("new"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dst", "a.txt"), []byte("old"), 0o600))
	return service, session, root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // 测试内的临时目录路径
	require.NoError(t, err)
	return string(data)
}

func TestPasteCopyOverwritesWhenTargetNameIsTheExistingName(t *testing.T) {
	service, session, root := pasteFixture(t)
	err := service.Paste(context.Background(), PasteRequest{
		TargetSessionID: session,
		TargetDir:       sftpiotest.RemotePath(filepath.Join(root, "dst")),
		Mode:            "copy",
		Items: []PasteItem{{
			SessionID:  session,
			Path:       sftpiotest.RemotePath(filepath.Join(root, "src", "a.txt")),
			TargetName: "a.txt",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "new", readFile(t, filepath.Join(root, "dst", "a.txt")))
	_, statErr := os.Stat(filepath.Join(root, "dst", "a_copy.txt"))
	assert.True(t, os.IsNotExist(statErr), "覆盖不得再静默改名")
}

func TestPasteCopyWritesToTheChosenKeepBothName(t *testing.T) {
	service, session, root := pasteFixture(t)
	err := service.Paste(context.Background(), PasteRequest{
		TargetSessionID: session,
		TargetDir:       sftpiotest.RemotePath(filepath.Join(root, "dst")),
		Mode:            "copy",
		Items: []PasteItem{{
			SessionID:  session,
			Path:       sftpiotest.RemotePath(filepath.Join(root, "src", "a.txt")),
			TargetName: "a_copy.txt",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "old", readFile(t, filepath.Join(root, "dst", "a.txt")))
	assert.Equal(t, "new", readFile(t, filepath.Join(root, "dst", "a_copy.txt")))
}

func TestPasteCutOverwritesExistingTarget(t *testing.T) {
	service, session, root := pasteFixture(t)
	err := service.Paste(context.Background(), PasteRequest{
		TargetSessionID: session,
		TargetDir:       sftpiotest.RemotePath(filepath.Join(root, "dst")),
		Mode:            "cut",
		Items: []PasteItem{{
			SessionID:  session,
			Path:       sftpiotest.RemotePath(filepath.Join(root, "src", "a.txt")),
			TargetName: "a.txt",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "new", readFile(t, filepath.Join(root, "dst", "a.txt")))
	_, statErr := os.Stat(filepath.Join(root, "src", "a.txt"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestPasteCopyRejectsCopyingAFileOntoItself(t *testing.T) {
	service, session, root := pasteFixture(t)
	err := service.Paste(context.Background(), PasteRequest{
		TargetSessionID: session,
		TargetDir:       sftpiotest.RemotePath(filepath.Join(root, "src")),
		Mode:            "copy",
		Items: []PasteItem{{
			SessionID:  session,
			Path:       sftpiotest.RemotePath(filepath.Join(root, "src", "a.txt")),
			TargetName: "a.txt",
		}},
	})
	require.Error(t, err)
	assert.Equal(t, "new", readFile(t, filepath.Join(root, "src", "a.txt")), "源文件不得被截断")
}

func TestPasteRejectsTargetNameThatIsNotABaseName(t *testing.T) {
	service, session, root := pasteFixture(t)
	for _, name := range []string{"", "../a.txt", "x/a.txt", ".", ".."} {
		err := service.Paste(context.Background(), PasteRequest{
			TargetSessionID: session,
			TargetDir:       sftpiotest.RemotePath(filepath.Join(root, "dst")),
			Mode:            "copy",
			Items: []PasteItem{{
				SessionID:  session,
				Path:       sftpiotest.RemotePath(filepath.Join(root, "src", "a.txt")),
				TargetName: name,
			}},
		})
		require.Error(t, err, "targetName %q", name)
	}
}
