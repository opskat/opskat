package localterm_svc

import (
	"strings"
	"testing"
)

// TestManager_SessionIDsSurviveRestart is the local-transport regression for
// issue #141: a fresh Manager (after an app restart) must not re-mint session
// IDs an earlier Manager already handed out, since IDs are persisted as
// frontend tab IDs.
func TestManager_SessionIDsSurviveRestart(t *testing.T) {
	before := NewManager()
	after := NewManager()

	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		id := before.nextSessionID()
		if !strings.HasPrefix(id, "local-") {
			t.Fatalf("session id %q lost the local- prefix; breaks transport inference", id)
		}
		seen[id] = struct{}{}
	}
	for i := 0; i < 50; i++ {
		id := after.nextSessionID()
		if _, dup := seen[id]; dup {
			t.Fatalf("after-restart id %q collides with a pre-restart id (issue #141)", id)
		}
	}
}
