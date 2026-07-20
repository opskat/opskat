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

// GetDocGate falls back to a process-wide default when ctx carries no gate — the real
// per-conversation wiring (injecting one via WithDocGate on each Send) is a later task;
// until then, callers still need a non-nil gate to consult.
func TestGetDocGate_NoInjectionFallsBackToProcessDefault(t *testing.T) {
	g := GetDocGate(context.Background())
	if g == nil {
		t.Fatal("GetDocGate must fall back to a process-wide default when ctx has no injected gate")
	}
	if got := GetDocGate(context.Background()); got != g {
		t.Fatal("GetDocGate must return the same default instance across calls")
	}
}

func TestWithDocGate_InjectedGateOverridesDefault(t *testing.T) {
	injected := NewDocGate()
	ctx := WithDocGate(context.Background(), injected)
	if got := GetDocGate(ctx); got != injected {
		t.Fatal("GetDocGate must return the ctx-injected gate, not the process default")
	}
}

// Callers must be able to explicitly opt out of gating (e.g. a future call site that
// wants no guidance behavior) by injecting a nil gate; GetDocGate must respect that
// nil rather than silently reverting to the process default.
func TestWithDocGate_ExplicitNilIsRespected(t *testing.T) {
	ctx := WithDocGate(context.Background(), nil)
	if got := GetDocGate(ctx); got != nil {
		t.Fatal("GetDocGate must return the explicitly injected nil, not fall back to the default")
	}
}
