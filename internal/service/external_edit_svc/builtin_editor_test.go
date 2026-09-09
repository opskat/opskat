package external_edit_svc

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/bootstrap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type launchRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *launchRecorder) Launch(path string, _ []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, path)
	return nil
}

func (r *launchRecorder) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func recordLaunches(h *rebindHarness) *launchRecorder {
	recorder := &launchRecorder{}
	h.svc.launch = recorder
	return recorder
}

func findEditor(editors []Editor, editorID string) *Editor {
	for _, editor := range editors {
		if editor.ID == editorID {
			cloned := editor
			return &cloned
		}
	}
	return nil
}

// gb18030 编码的“中文”，用于验证内置编辑器读写两侧都按会话记录的编码转换。
var gb18030Chinese = []byte{0xd6, 0xd0, 0xce, 0xc4}

func openBuiltInSession(t *testing.T, h *rebindHarness, remotePath string, data []byte) *Session {
	t.Helper()
	h.remote.SetFile("ssh-a", remotePath, data, remotePath)
	session, err := h.svc.Open(context.Background(), OpenRequest{
		AssetID:    101,
		SessionID:  "ssh-a",
		RemotePath: remotePath,
		EditorID:   builtInEditorID,
	})
	require.NoError(t, err)
	require.NotNil(t, session)
	return session
}

func TestOpenWithBuiltInEditorSkipsLaunchAndUsesExplicitSaveMode(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	recorder := recordLaunches(h)

	session := openBuiltInSession(t, h, "/etc/nginx/nginx.conf", []byte("worker_processes 1;\n"))

	assert.Empty(t, recorder.Calls())
	assert.Equal(t, builtInEditorID, session.EditorID)
	assert.Equal(t, saveModeManualExplicit, session.SaveMode)
}

func TestOpenWithExternalEditorStillLaunchesProcess(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	recorder := recordLaunches(h)

	session := h.openSession(t, "ssh-a", "/etc/hosts", "/etc/hosts", []byte("127.0.0.1 localhost\n"))

	assert.Len(t, recorder.Calls(), 1)
	assert.Equal(t, saveModeAutoLive, session.SaveMode)
}

func TestRecoverBuiltInEditorSessionSkipsLaunch(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	session := openBuiltInSession(t, h, "/etc/nginx/nginx.conf", []byte("worker_processes 1;\n"))
	recorder := recordLaunches(h)

	recovered, err := h.svc.Recover(session.ID)

	require.NoError(t, err)
	require.NotNil(t, recovered)
	assert.Empty(t, recorder.Calls())
}

func TestBuiltInEditorIsAlwaysAvailableAndSelectableAsDefault(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })

	settings, err := h.svc.GetSettings()
	require.NoError(t, err)
	builtIn := findEditor(settings.Editors, builtInEditorID)
	require.NotNil(t, builtIn, "built-in editor must be listed among the editors")
	assert.True(t, builtIn.BuiltIn)
	assert.True(t, builtIn.Available)
	assert.Empty(t, builtIn.Path)

	saved, err := h.svc.SaveSettings(SettingsInput{
		DefaultEditorID:      builtInEditorID,
		WorkspaceRoot:        h.manifest,
		CleanupRetentionDays: 7,
		MaxReadFileSizeMB:    10,
	})
	require.NoError(t, err)
	assert.Equal(t, builtInEditorID, saved.DefaultEditorID)
	assert.Equal(t, builtInEditorID, h.cfg.ExternalEditDefaultEditorID)
}

func TestImplicitDefaultEditorKeepsPreferringInstalledExternalEditor(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	execPath := filepath.Join(t.TempDir(), "editor.exe")
	require.NoError(t, os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o700))
	h.cfg.ExternalEditDefaultEditorID = ""
	h.cfg.ExternalEditCustomEditors = []bootstrap.ExternalEditorConfig{
		{ID: "custom-1", Name: "My Editor", Path: execPath},
	}

	settings, err := h.svc.GetSettings()
	require.NoError(t, err)
	require.NotEmpty(t, settings.DefaultEditorID)
	assert.NotEqual(t, builtInEditorID, settings.DefaultEditorID)

	assert.Equal(t, builtInEditorID, firstAvailableEditorID([]Editor{
		{ID: builtInEditorID, Available: true},
		{ID: "vscode"},
	}))
}

func TestReadSessionTextDecodesLocalCopyWithSessionEncoding(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	recordLaunches(h)
	session := openBuiltInSession(t, h, "/srv/app/app.conf", gb18030Chinese)
	require.Equal(t, textEncodingGB18030, session.OriginalEncoding)

	text, err := h.svc.ReadSessionText(session.ID)

	require.NoError(t, err)
	assert.Equal(t, "中文", text)
}

func TestSaveSessionTextWritesRemoteInSessionEncoding(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	recordLaunches(h)
	session := openBuiltInSession(t, h, "/srv/app/app.conf", gb18030Chinese)

	result, err := h.svc.SaveSessionText(context.Background(), SaveSessionTextRequest{
		SessionID: session.ID,
		Text:      "中文中文",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, saveStatusSaved, result.Status)
	remote, _, err := h.remote.ReadFile("ssh-a", "/srv/app/app.conf")
	require.NoError(t, err)
	assert.Equal(t, append(append([]byte(nil), gb18030Chinese...), gb18030Chinese...), remote)
}

func TestSaveSessionTextReturnsConflictWithoutWritingWhenRemoteChanged(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	recordLaunches(h)
	session := openBuiltInSession(t, h, "/etc/nginx/nginx.conf", []byte("worker_processes 1;\n"))
	h.remote.SetFile("ssh-a", "/etc/nginx/nginx.conf", []byte("worker_processes 8;\n"), "/etc/nginx/nginx.conf")

	result, err := h.svc.SaveSessionText(context.Background(), SaveSessionTextRequest{
		SessionID: session.ID,
		Text:      "worker_processes 2;\n",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, saveStatusConflict, result.Status)
	require.NotNil(t, result.Conflict)
	assert.Equal(t, session.ID, result.Conflict.PrimaryDraftSessionID)
	assert.Empty(t, h.remote.Writes())
	remote, _, err := h.remote.ReadFile("ssh-a", "/etc/nginx/nginx.conf")
	require.NoError(t, err)
	assert.Equal(t, "worker_processes 8;\n", string(remote))
}

func TestSaveSessionTextRejectsNonTextContent(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })
	recordLaunches(h)
	session := openBuiltInSession(t, h, "/etc/nginx/nginx.conf", []byte("worker_processes 1;\n"))

	_, err := h.svc.SaveSessionText(context.Background(), SaveSessionTextRequest{
		SessionID: session.ID,
		Text:      "worker\x00processes",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "文本")
	assert.Empty(t, h.remote.Writes())
}

func TestSessionTextEntriesRejectEmptyAndUnknownSession(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-a"} })

	_, err := h.svc.ReadSessionText("  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionId 不能为空")

	_, err = h.svc.ReadSessionText("missing-session")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "外部编辑会话不存在")

	_, err = h.svc.SaveSessionText(context.Background(), SaveSessionTextRequest{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionId 不能为空")

	_, err = h.svc.SaveSessionText(context.Background(), SaveSessionTextRequest{SessionID: "missing-session", Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "外部编辑会话不存在")
}
