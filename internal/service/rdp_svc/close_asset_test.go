package rdp_svc

import (
	"testing"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newIdleClient 造一个未拨号的 RDP client：Close 只关 done 通道，不碰网络。
func newIdleClient(t *testing.T) *rdp.Client {
	t.Helper()
	opts := rdp.DefaultOptions()
	opts.Host = "127.0.0.1"
	client, err := rdp.NewClient(opts)
	require.NoError(t, err)
	return client
}

func isDone(c *rdp.Client) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

func TestService_CloseAsset(t *testing.T) {
	svc := New(nil, nil)
	a1, a2, b1 := newIdleClient(t), newIdleClient(t), newIdleClient(t)
	svc.sessions["a1"] = &session{id: "a1", assetID: 1, client: a1, done: make(chan struct{})}
	svc.sessions["a2"] = &session{id: "a2", assetID: 1, client: a2, done: make(chan struct{})}
	svc.sessions["b1"] = &session{id: "b1", assetID: 2, client: b1, done: make(chan struct{})}

	assert.NoError(t, svc.CloseAsset(1))

	assert.True(t, isDone(a1))
	assert.True(t, isDone(a2))
	assert.False(t, isDone(b1), "其它资产的会话不应被关闭")
}
