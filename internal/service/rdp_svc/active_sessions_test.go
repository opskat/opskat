package rdp_svc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestServiceActiveSessionsAreSortedAndRemovedOnCloseSession(t *testing.T) {
	svc := New(nil, nil)
	started := time.Unix(123, 0)
	z := &session{id: "z", assetID: 2, client: newIdleClient(t), done: make(chan struct{}), startedAt: started}
	a := &session{id: "a", assetID: 1, client: newIdleClient(t), done: make(chan struct{}), startedAt: started.Add(time.Second)}
	svc.sessions[z.id] = z
	svc.sessions[a.id] = a

	assert.Equal(t, []SessionActivity{
		{SessionID: "a", AssetID: 1, StartedAt: started.Add(time.Second)},
		{SessionID: "z", AssetID: 2, StartedAt: started},
	}, svc.ActiveSessions())

	assert.NoError(t, svc.Close("a"))
	assert.Equal(t, []SessionActivity{{SessionID: "z", AssetID: 2, StartedAt: started}}, svc.ActiveSessions())
}
