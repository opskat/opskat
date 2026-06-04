package ssh

import (
	"context"
	"strings"
	"testing"
)

// TestConnectionIDsSurviveRestart is the connecting-phase variant of issue #141:
// a connection ID is the frontend tab id while a connection is establishing, so
// it can be persisted and restored. A fresh binder (after an app restart) must
// not re-mint connection IDs an earlier run already handed out, or a restored
// connecting tab collides with a new one. Tests through New to also catch a
// forgotten generator init (nil-deref in production).
func TestConnectionIDsSurviveRestart(t *testing.T) {
	before := New(context.TODO(), nil, nil, nil, nil)
	after := New(context.TODO(), nil, nil, nil, nil) // models the process after a restart

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
