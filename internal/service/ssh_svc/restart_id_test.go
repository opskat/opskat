package ssh_svc

import (
	"strings"
	"testing"
)

// TestManager_SessionIDsSurviveRestart is the manager-level regression for
// issue #141: session IDs are persisted as frontend tab IDs, so IDs from a
// fresh Manager (after an app restart, counter back at 0) must not collide with
// IDs an earlier Manager already handed out.
func TestManager_SessionIDsSurviveRestart(t *testing.T) {
	before := NewManager()
	after := NewManager() // models the process after a restart

	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		id := before.nextSessionID()
		if !strings.HasPrefix(id, "ssh-") {
			t.Fatalf("session id %q lost the ssh- prefix; breaks transport inference", id)
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
