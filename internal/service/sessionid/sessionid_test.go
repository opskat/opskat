package sessionid

import (
	"strings"
	"testing"
)

// TestGenerator_IDsDisjointAcrossInstances is the regression for issue #141:
// terminal session IDs are persisted into the frontend tab store, but the
// generating counter lives in process memory and resets on every app restart.
// Two independently-created generators model "before restart" and "after
// restart"; their IDs should not overlap unless the random instance segment
// collides, otherwise a freshly minted session ID collides with a stale
// persisted tab ID and two tabs end up sharing one ID.
func TestGenerator_IDsDisjointAcrossInstances(t *testing.T) {
	prev := NewGenerator("ssh") // previous app run
	next := NewGenerator("ssh") // after restart — counter is back at 0

	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id := prev.Next()
		if _, dup := seen[id]; dup {
			t.Fatalf("previous-run generator produced duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
	for i := 0; i < 100; i++ {
		id := next.Next()
		if _, dup := seen[id]; dup {
			t.Fatalf("after-restart id %q collides with a previous-run id", id)
		}
		seen[id] = struct{}{}
	}
}

// TestGenerator_IDsUniqueWithinInstance guards the basic monotonic-counter
// contract: a single generator never repeats an id.
func TestGenerator_IDsUniqueWithinInstance(t *testing.T) {
	g := NewGenerator("ssh")
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		id := g.Next()
		if _, dup := seen[id]; dup {
			t.Fatalf("generator repeated id %q at iteration %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

// TestGenerator_PreservesKindPrefix protects the frontend's transport inference,
// which keys off the "ssh-" / "serial-" / "local-" prefix
// (inferTransportFromSessionId). The kind must remain the leading segment.
func TestGenerator_PreservesKindPrefix(t *testing.T) {
	for _, kind := range []string{"ssh", "serial", "local"} {
		id := NewGenerator(kind).Next()
		if !strings.HasPrefix(id, kind+"-") {
			t.Errorf("kind %q: id %q does not start with %q-", kind, id, kind)
		}
	}
}
