package serial

import (
	"context"
	"strings"
	"testing"
)

// TestConnectionIDsSurviveRestart is the serial connecting-phase variant of
// issue #141: a fresh binder (after an app restart) must not re-mint connection
// IDs an earlier run already handed out, since a connecting tab's id is the
// connection ID and can be persisted.
func TestConnectionIDsSurviveRestart(t *testing.T) {
	before := New(context.TODO(), nil, nil)
	after := New(context.TODO(), nil, nil)

	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		id := before.nextConnectionID()
		if !strings.HasPrefix(id, "conn-") {
			t.Fatalf("connection id %q lost the conn- prefix", id)
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
