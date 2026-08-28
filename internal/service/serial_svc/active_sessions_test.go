package serial_svc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestManagerActiveSessionsAreSortedAndExcludeClosed(t *testing.T) {
	m := NewManager()
	started := time.Unix(123, 0)
	m.sessions.Store("z", &Session{ID: "z", AssetID: 2, startedAt: started})
	a := &Session{ID: "a", AssetID: 1, startedAt: started.Add(time.Second)}
	m.sessions.Store("a", a)

	assert.Equal(t, []SessionActivity{
		{SessionID: "a", AssetID: 1, StartedAt: started.Add(time.Second)},
		{SessionID: "z", AssetID: 2, StartedAt: started},
	}, m.ActiveSessions())

	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	assert.Equal(t, []SessionActivity{{SessionID: "z", AssetID: 2, StartedAt: started}}, m.ActiveSessions())
}
