package local

import (
	"context"
	"strings"
	"testing"
)

// TestConnectionIDsSurviveRestart is the local connecting-phase variant of
// issue #141: a fresh binder (after an app restart) must not re-mint connection
// IDs an earlier run already handed out. The "local-" prefix is preserved
// because restore infers the transport from it (inferTransportFromSessionId).
func TestConnectionIDsSurviveRestart(t *testing.T) {
	before := New(context.TODO(), nil, nil)
	after := New(context.TODO(), nil, nil)

	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		id := before.nextConnectionID()
		if !strings.HasPrefix(id, "local-conn-") {
			t.Fatalf("connection id %q lost the local-conn- prefix", id)
		}
		seen[id] = struct{}{}
	}
	for i := 0; i < 50; i++ {
		id := after.nextConnectionID()
		if _, dup := seen[id]; dup {
			t.Fatalf("after-restart connection id %q collides with a pre-restart id (issue #141)", id)
		}
	}
}
