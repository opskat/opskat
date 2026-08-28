package ssh_svc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestManagerActiveSessionsListsInteractiveSessionsOnly(t *testing.T) {
	m := NewManager()
	started := time.Unix(123, 0)
	m.sessions.Store("z", &Session{ID: "z", AssetID: 2, interactive: true, startedAt: started})
	a := &Session{ID: "a", AssetID: 1, interactive: true, startedAt: started.Add(time.Second)}
	m.sessions.Store("a", a)
	m.sessions.Store("sftp", &Session{ID: "sftp", AssetID: 3})

	assert.Equal(t, []SessionActivity{
		{SessionID: "a", AssetID: 1, StartedAt: started.Add(time.Second)},
		{SessionID: "z", AssetID: 2, StartedAt: started},
	}, m.ActiveSessionDetails())

	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	assert.Equal(t, []SessionActivity{{SessionID: "z", AssetID: 2, StartedAt: started}}, m.ActiveSessionDetails())
}
