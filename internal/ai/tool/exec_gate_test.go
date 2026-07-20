package tool

import "testing"

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
