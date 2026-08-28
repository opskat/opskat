package vnc_svc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestManagerActiveSessionsAreSortedAndContainNoConnectionSecrets(t *testing.T) {
	m := &Manager{sessions: make(map[string]*Session)}
	started := time.Unix(123, 0)
	m.sessions["z"] = &Session{ID: "z", assetID: 2, startedAt: started, Username: "user", Password: "secret"}
	m.sessions["a"] = &Session{ID: "a", assetID: 1, startedAt: started.Add(time.Second), Password: "other"}

	assert.Equal(t, []SessionActivity{
		{SessionID: "a", AssetID: 1, StartedAt: started.Add(time.Second)},
		{SessionID: "z", AssetID: 2, StartedAt: started},
	}, m.ActiveSessions())

	m.Disconnect("a")
	assert.Equal(t, []SessionActivity{{SessionID: "z", AssetID: 2, StartedAt: started}}, m.ActiveSessions())
}
