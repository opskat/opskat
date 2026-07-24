package vnc_svc

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// pipeConn 返回一端 net.Conn 与一个探测它是否已关闭的函数。
// net.Pipe 是同步的，对端必须一直在读，否则探测用的 Write 会阻塞。
func pipeConn(t *testing.T) (net.Conn, func() bool) {
	t.Helper()
	local, remote := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, remote) }()
	t.Cleanup(func() { _ = remote.Close() })
	return local, func() bool {
		_, err := local.Write([]byte{0})
		return err != nil
	}
}

func TestManager_CloseAsset(t *testing.T) {
	m := NewManager(nil)
	connA, closedA := pipeConn(t)
	connB, closedB := pipeConn(t)

	m.sessions["a"] = &Session{ID: "a", assetID: 1, conn: connA}
	m.sessions["b"] = &Session{ID: "b", assetID: 2, conn: connB}

	m.CloseAsset(1)

	assert.NotContains(t, m.sessions, "a")
	assert.Contains(t, m.sessions, "b")
	assert.True(t, closedA(), "该资产的 VNC 连接应已关闭")
	assert.False(t, closedB(), "其它资产的 VNC 连接不应受影响")
}
