package runner

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
)

type syncSink struct {
	mu  sync.Mutex
	out []StreamEvent
}

func (s *syncSink) emit(e StreamEvent) {
	s.mu.Lock()
	s.out = append(s.out, e)
	s.mu.Unlock()
}

func (s *syncSink) events() []StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StreamEvent(nil), s.out...)
}

func joinContent(evs []StreamEvent, typ string) string {
	var sb strings.Builder
	for _, e := range evs {
		if e.Type == typ {
			sb.WriteString(e.Content)
		}
	}
	return sb.String()
}

func TestEventTranslator_RapidDeltasCoalesced(t *testing.T) {
	tr := NewStreamTranslator()
	sink := &syncSink{}
	var want strings.Builder
	for i := 0; i < 100; i++ {
		d := string(rune('a' + i%26))
		want.WriteString(d)
		tr.Translate(agent.Event{Kind: agent.EventTextDelta, Delta: d}, sink.emit)
	}
	time.Sleep(200 * time.Millisecond) // 超过合并窗口，由定时器 flush
	evs := sink.events()
	t.Logf("100 rapid deltas -> %d events", len(evs))
	if len(evs) == 0 || len(evs) > 5 {
		t.Fatalf("want 1..5 events, got %d", len(evs))
	}
	if got := joinContent(evs, "content"); got != want.String() {
		t.Fatalf("content mismatch: %q vs %q", got, want.String())
	}
}

func TestEventTranslator_TextFlushedBeforeToolEvent(t *testing.T) {
	tr := NewStreamTranslator()
	sink := &syncSink{}
	tr.Translate(agent.Event{Kind: agent.EventTextDelta, Delta: "hel"}, sink.emit)
	tr.Translate(agent.Event{Kind: agent.EventTextDelta, Delta: "lo"}, sink.emit)
	tr.Translate(agent.Event{Kind: agent.EventPreToolUse, Tool: &agent.ToolEvent{Name: "x", ToolUseID: "1"}}, sink.emit)
	evs := sink.events()
	if len(evs) != 2 || evs[0].Type != "content" || evs[0].Content != "hello" || evs[1].Type != "tool_start" {
		t.Fatalf("unexpected order: %+v", evs)
	}
}

func TestEventTranslator_ThinkingContentInterleaveKeepsOrder(t *testing.T) {
	tr := NewStreamTranslator()
	sink := &syncSink{}
	for _, ev := range []agent.Event{
		{Kind: agent.EventThinkingDelta, Delta: "t1"},
		{Kind: agent.EventThinkingDelta, Delta: "t2"},
		{Kind: agent.EventTextDelta, Delta: "c1"},
		{Kind: agent.EventTextDelta, Delta: "c2"},
		{Kind: agent.EventDone},
	} {
		tr.Translate(ev, sink.emit)
	}
	evs := sink.events()
	got := make([]string, 0, len(evs))
	for _, e := range evs {
		got = append(got, e.Type+":"+e.Content)
	}
	want := []string{"thinking:t1t2", "thinking_done:", "content:c1c2", "done:"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEventTranslator_CloseFlushesTailAndStopsTimer(t *testing.T) {
	tr := NewStreamTranslator()
	sink := &syncSink{}
	tr.Translate(agent.Event{Kind: agent.EventTextDelta, Delta: "tail"}, sink.emit)
	tr.Close()
	if evs := sink.events(); len(evs) != 1 || evs[0].Content != "tail" {
		t.Fatalf("close must flush tail: %+v", evs)
	}
	time.Sleep(150 * time.Millisecond)
	if n := len(sink.events()); n != 1 {
		t.Fatalf("emitted after close: %d events", n)
	}
}

func TestEventTranslator_DeltaAfterCloseIsDropped(t *testing.T) {
	tr := NewStreamTranslator()
	sink := &syncSink{}
	tr.Close()
	tr.Translate(agent.Event{Kind: agent.EventTextDelta, Delta: "late"}, sink.emit)
	time.Sleep(150 * time.Millisecond)
	if n := len(sink.events()); n != 0 {
		t.Fatalf("emitted after close: %d", n)
	}
}
