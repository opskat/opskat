package external_edit_svc

import (
	"context"
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
	// 决策 13：两类失败的出路不同 —— 不可达要重连该资产，不是同一份文件才要重新打开文件。
	// 出路说明若还是「重新打开该远程文件」，分类就没有走到用户面前。
	require.Contains(t, err.Error(), externalEditUnreachableHint)
	require.NotContains(t, err.Error(), externalEditReconnectHint)
}

// 远端确实被换成另一份文件（同路径、不同真实路径）时仍必须报路径身份失败。
func TestRefreshDifferentRemoteDocumentStillReportsPathIdentity(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return nil })
	session := h.openSession(t, "sess-1", "/srv/app.conf", "/srv/app.conf", []byte("alpha\n"))

	h.remote.SetFile("sess-1", "/srv/app.conf", []byte("beta\n"), "/srv/replacement.conf")

	_, err := h.svc.Refresh(session.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "当前文件位置已变化")
	require.Contains(t, err.Error(), externalEditReconnectHint)
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

// 经软链打开的文件在远端消失时同样是「无法确认仍是同一份远程文件」这一类结论：
// 它必须走进 buildErrorSnapshot 的分类里，而不是掉到兜底的「同步失败，请稍后重试」。
func TestSaveRecordsClassifiedSnapshotWhenSymlinkedRemoteVanishes(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return nil })
	session := h.openSession(t, "ssh-1", "/srv/link.conf", "/srv/real.conf", []byte("alpha\n"))
	markDirtyLocalCopy(t, session, []byte("alpha dirty\n"))

	h.remote.SetError("ssh-1", "/srv/link.conf", errors.New("no such file"))

	_, err := h.svc.Save(context.Background(), session.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "无法确认仍是同一份远程文件")

	snapshot := h.refreshSession(t, session.ID).LastError
	require.NotNil(t, snapshot)
	require.Equal(t, "当前文件暂时无法继续同步", snapshot.Summary)
	require.Equal(t, externalEditReconnectHint, snapshot.Suggestion)
}
