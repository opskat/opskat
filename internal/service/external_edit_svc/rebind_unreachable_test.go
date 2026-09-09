package external_edit_svc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 应用重启后持久化的 SSH 会话 id 必然已死：候选会话连不上时必须报「会话不可达」，
// 而不是误报成「远程路径已不是同一份文件」——两者的出路不同（重连 vs 重新打开文件）。
func TestRefreshUnreachableCandidateReportsUnreachableNotPathIdentity(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return nil })
	session := h.openSession(t, "sess-dead", "/srv/app.conf", "/srv/app.conf", []byte("alpha\n"))

	h.remote.SetError("sess-dead", "/srv/app.conf", errors.New("ssh会话不存在: sess-dead"))

	_, err := h.svc.Refresh(session.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "当前远程文件已不可访问")
	require.NotContains(t, err.Error(), "当前文件位置已变化")
}

// 远端确实被换成另一份文件（同路径、不同真实路径）时仍必须报路径身份失败。
func TestRefreshDifferentRemoteDocumentStillReportsPathIdentity(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return nil })
	session := h.openSession(t, "sess-1", "/srv/app.conf", "/srv/app.conf", []byte("alpha\n"))

	h.remote.SetFile("sess-1", "/srv/app.conf", []byte("beta\n"), "/srv/replacement.conf")

	_, err := h.svc.Refresh(session.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "当前文件位置已变化")
}

// 一个候选不可达、另一个可达但已是另一份文件时，可达候选的结论仍要抛出——
// 「不可达不计入 reachableDifferentDocument」不能把真正的路径身份失败一起抹掉。
func TestRefreshUnreachableCandidateDoesNotMaskPathIdentityFailure(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"sess-live"} })
	session := h.openSession(t, "sess-dead", "/srv/app.conf", "/srv/app.conf", []byte("alpha\n"))

	h.remote.SetError("sess-dead", "/srv/app.conf", errors.New("ssh会话不存在: sess-dead"))
	h.remote.SetFile("sess-live", "/srv/app.conf", []byte("beta\n"), "/srv/replacement.conf")

	_, err := h.svc.Refresh(session.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "当前文件位置已变化")
}
