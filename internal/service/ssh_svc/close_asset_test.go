package ssh_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// newDetachedSession 造一个不带真实连接的会话。refCount 取 2，使 Close 里的
// shared.release() 不会走到关闭底层 *ssh.Client 的分支（测试没有真连接可关）。
func newDetachedSession(id string, assetID int64) *Session {
	return &Session{ID: id, AssetID: assetID, shared: &sharedClient{refCount: 2}}
}

func TestManager_CloseAsset(t *testing.T) {
	m := NewManager()
	m.sessions.Store("a1", newDetachedSession("a1", 1))
	m.sessions.Store("a2", newDetachedSession("a2", 1))
	m.sessions.Store("b1", newDetachedSession("b1", 2))

	m.CloseAsset(1)

	_, okA1 := m.GetSession("a1")
	_, okA2 := m.GetSession("a2")
	other, okB1 := m.GetSession("b1")
	assert.False(t, okA1)
	assert.False(t, okA2)
	assert.True(t, okB1)
	assert.False(t, other.IsClosed(), "其它资产的会话不受影响")
}
