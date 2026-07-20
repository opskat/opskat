package tool

import (
	"context"
	"testing"
)

func TestDocGate_UnmarkedTypeIsNotDocumented(t *testing.T) {
	g := NewDocGate()
	if g.IsDocumented(1, "redis") {
		t.Fatal("unmarked type reported as documented")
	}
}

func TestDocGate_MarkThenDocumented(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	if !g.IsDocumented(1, "redis") {
		t.Fatal("marked type reported as undocumented")
	}
}

func TestDocGate_ScopedPerType(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	if g.IsDocumented(1, "database") {
		t.Fatal("marking redis must not document database")
	}
}

func TestDocGate_ScopedPerConversation(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	if g.IsDocumented(2, "redis") {
		t.Fatal("marking conversation 1 must not document conversation 2")
	}
}

func TestDocGate_Reset(t *testing.T) {
	g := NewDocGate()
	g.MarkDocumented(1, "redis")
	g.Reset(1)
	if g.IsDocumented(1, "redis") {
		t.Fatal("Reset did not clear the conversation")
	}
}

// GetDocGate returns nil when ctx carries no injected gate — there is no process-wide
// default. There used to be one (a package-level singleton every conversation shared),
// but it made GetDocGate's result depend on which test/conversation happened to run
// first: two tests using bare context.Background() would silently read and write the
// same gate, so `go test -shuffle=on` failed 5 of 6 runs depending on ordering. Callers
// must treat nil as "allow" — DocGate is a guidance mechanism, not the security
// boundary; the permission check is. Production injects a per-conversation gate via
// WithDocGate on each Send (internal/app/ai); callers without one — opsctl, tests — get
// no gating at all, which is safe precisely because it is not the boundary.
func TestGetDocGate_NoInjectionReturnsNil(t *testing.T) {
	if got := GetDocGate(context.Background()); got != nil {
		t.Fatalf("GetDocGate must return nil when ctx has no injected gate, got %v", got)
	}
}

func TestWithDocGate_InjectedGateIsReturned(t *testing.T) {
	injected := NewDocGate()
	ctx := WithDocGate(context.Background(), injected)
	if got := GetDocGate(ctx); got != injected {
		t.Fatal("GetDocGate must return the ctx-injected gate")
	}
}

// Callers must be able to explicitly opt out of gating (e.g. a call site that wants no
// guidance behavior) by injecting a nil gate; GetDocGate must return that nil rather than
// treating "found a nil value" differently from "found nothing" — both mean allow.
func TestWithDocGate_ExplicitNilIsRespected(t *testing.T) {
	ctx := WithDocGate(context.Background(), nil)
	if got := GetDocGate(ctx); got != nil {
		t.Fatal("GetDocGate must return nil for an explicitly injected nil gate")
	}
}
