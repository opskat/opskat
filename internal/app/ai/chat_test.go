package ai

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo/mock_custom_type_repo"
)

func TestFinishRunnerRemovesOnlyCompletedEntry(t *testing.T) {
	a := &AI{}
	completed := &runnerEntry{}
	replacement := &runnerEntry{}

	a.runners.Store(int64(7), completed)
	a.finishRunner(7, completed)
	if got := a.ActiveTaskCount(); got != 0 {
		t.Fatalf("completed runner remains active: got %d", got)
	}

	a.runners.Store(int64(7), replacement)
	a.finishRunner(7, completed)
	if got := a.ActiveTaskCount(); got != 1 {
		t.Fatalf("stale completion removed replacement runner: got %d", got)
	}
}

// recordingEntry 返回一个「这一轮还没收尾」的 runnerEntry（done 不关闭，模拟卡住的工具），
// 以及它发往前端的事件记录。
func recordingEntry(t *testing.T) (*runnerEntry, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	e := &runnerEntry{
		convID: 42,
		done:   make(chan struct{}),
		emitFn: func(ev runner.StreamEvent) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, ev.Type)
		},
	}
	return e, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func shortStopGrace(t *testing.T) {
	t.Helper()
	old := stopGrace
	stopGrace = 50 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })
}

func TestStopEntryEmitsStoppedThenDoneWhenTurnDoesNotWindDown(t *testing.T) {
	shortStopGrace(t)
	e, events := recordingEntry(t)
	e.deliver(runner.StreamEvent{Type: "tool_start"})

	(&AI{}).stopEntry(e)

	if got := strings.Join(events(), ","); got != "tool_start,stopped,done" {
		t.Fatalf("events after stop grace: got %q, want tool_start,stopped,done", got)
	}

	// 卡住的工具迟迟返回后，这一轮自己的收尾不能再到达前端（否则会结束同会话的新一轮）。
	e.deliver(runner.StreamEvent{Type: "tool_result"})
	e.deliver(runner.StreamEvent{Type: "stopped"})
	e.deliver(runner.StreamEvent{Type: "done"})
	if got := strings.Join(events(), ","); got != "tool_start,stopped,done" {
		t.Fatalf("late events leaked from abandoned turn: got %q", got)
	}
}

func TestStopEntryOnlyAddsDoneWhenStoppedAlreadyDelivered(t *testing.T) {
	shortStopGrace(t)
	e, events := recordingEntry(t)
	e.deliver(runner.StreamEvent{Type: "stopped"})

	(&AI{}).stopEntry(e)

	if got := strings.Join(events(), ","); got != "stopped,done" {
		t.Fatalf("got %q, want stopped,done", got)
	}
}

func TestStopEntryAddsNothingWhenTurnAlreadyFinished(t *testing.T) {
	shortStopGrace(t)
	for _, terminal := range []string{"done", "error"} {
		e, events := recordingEntry(t)
		e.deliver(runner.StreamEvent{Type: terminal})

		(&AI{}).stopEntry(e)

		if got := strings.Join(events(), ","); got != terminal {
			t.Fatalf("after %s: got %q, want no synthetic events", terminal, got)
		}
	}
}

func TestStopEntryAddsNothingWhenTurnWindsDownInTime(t *testing.T) {
	shortStopGrace(t)
	e, events := recordingEntry(t)
	go func() {
		e.deliver(runner.StreamEvent{Type: "stopped"})
		e.deliver(runner.StreamEvent{Type: "done"})
		close(e.done)
	}()

	(&AI{}).stopEntry(e)

	if got := strings.Join(events(), ","); got != "stopped,done" {
		t.Fatalf("got %q, want the turn's own stopped,done only", got)
	}
}

// 开始新一轮时旧一轮还登记着（例如前端重载后状态丢失）：前端已为新一轮换上同一事件名的监听，
// 旧一轮的任何事件——包括宽限超时后的收尾——都会被当成新一轮的，必须一律不送达。
func TestReplaceRunnerKeepsStuckOldTurnAwayFromTheNewTurn(t *testing.T) {
	shortStopGrace(t)
	old, events := recordingEntry(t)
	a := &AI{}
	a.runners.Store(old.convID, old)

	a.replaceRunner(old.convID)

	old.deliver(runner.StreamEvent{Type: "tool_result"})
	old.deliver(runner.StreamEvent{Type: "done"})
	if got := strings.Join(events(), ","); got != "" {
		t.Fatalf("replaced turn reached the new turn's listener: got %q", got)
	}
	if got := a.ActiveTaskCount(); got != 0 {
		t.Fatalf("replaced runner remains registered: got %d", got)
	}
}

// 停止宽限中（StopAIGeneration 还在等卡住的这一轮）同会话就开始了新一轮（例如编辑重发、前端重载后
// 直接发送）：这一轮已不在运行表里，但宽限超时后代发的收尾仍会落到新一轮同名的监听上，必须截断。
func TestNewTurnDuringStopGraceKeepsTheAbandonedTerminalAway(t *testing.T) {
	old := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })
	stuck, events := recordingEntry(t)
	a := &AI{}
	a.runners.Store(stuck.convID, stuck)

	stopReturned := make(chan struct{})
	go func() {
		_ = a.StopAIGeneration(stuck.convID)
		close(stopReturned)
	}()
	for a.ActiveTaskCount() != 0 {
		time.Sleep(time.Millisecond)
	}

	a.replaceRunner(stuck.convID)
	<-stopReturned

	if got := strings.Join(events(), ","); got != "" {
		t.Fatalf("the abandoned turn's terminal reached the new turn's listener: got %q", got)
	}
}

// 连点停止（两次 StopAIGeneration 并发）时，没抢到这一轮的那次不能撤掉另一次在宽限里的登记，
// 否则宽限中开始的新一轮找不到旧一轮，宽限超时后代发的收尾会落到新一轮上。两次并发停止都读到
// 这一轮的交错要靠多会话并发碰出来。
func TestNewTurnDuringStopGraceAfterConcurrentStopsKeepsTheAbandonedTerminalAway(t *testing.T) {
	old := stopGrace
	stopGrace = time.Second // 宽限要盖过 1500 个 goroutine 的调度延迟，新一轮才一定开始在宽限内
	t.Cleanup(func() { stopGrace = old })
	a := &AI{}

	const convs = 500
	leaked := make(chan struct{}, convs)
	var wg sync.WaitGroup
	for i := range convs {
		stuck, events := recordingEntry(t)
		stuck.convID = int64(1000 + i)
		a.runners.Store(stuck.convID, stuck)
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := make(chan struct{})
			returned := make(chan struct{}, 2)
			for range 2 {
				go func() {
					<-start
					_ = a.StopAIGeneration(stuck.convID)
					returned <- struct{}{}
				}()
			}
			close(start)
			<-returned // 没抢到这一轮的那次先返回，抢到的那次还在宽限里
			a.replaceRunner(stuck.convID)
			<-returned
			if len(events()) > 0 {
				leaked <- struct{}{}
			}
		}()
	}
	wg.Wait()
	if n := len(leaked); n > 0 {
		t.Fatalf("%d of %d conversations: the abandoned turn's terminal reached the new turn's listener", n, convs)
	}
}

func TestActiveTasksReturnsStableConversationIDs(t *testing.T) {
	a := &AI{}
	a.runners.Store(int64(9), &runnerEntry{})
	a.runners.Store(int64(2), &runnerEntry{})

	got := ActiveTasks(a)
	if len(got) != 2 || got[0] != 2 || got[1] != 9 {
		t.Fatalf("ActiveTasks() = %v, want [2 9]", got)
	}
}

func TestOutwardFailurePreservesRawTextOnEventAndReturnedWailsError(t *testing.T) {
	err := errors.New("Authorization: Basic wails-return-secret")
	message, returned := outwardFailure("send to LLM", err)
	for name, text := range map[string]string{"event": message, "returned error": returned.Error()} {
		if !strings.Contains(text, "wails-return-secret") {
			t.Fatalf("%s lost the raw provider error: %s", name, text)
		}
	}
	if !strings.Contains(returned.Error(), "send to LLM") {
		t.Fatalf("returned Wails error lost its operation context: %s", returned)
	}
	if returned.Error() != "send to LLM: "+err.Error() {
		t.Fatalf("returned Wails error must be prefix + raw error text: %s", returned)
	}
}

// TestAllBuiltinAssetTypeSkills covers the listing that feeds PromptBuilder's per-type
// discovery section. It is deliberately independent of openTabs: the listing exists so
// the model learns that help(asset) exists and which types exec covers, which a session
// with no matching tab open needs just as much. (It does not satisfy the exec doc gate —
// only an explicit help call does; see internal/ai/tool.DocGate.)
func TestAllBuiltinAssetTypeSkills(t *testing.T) {
	t.Run("every built-in type is included, with no tabs involved", func(t *testing.T) {
		got := allAssetTypeSkills()
		// The 8 exec types (with "## Command syntax") plus the 4 doc-only types
		// (rdp/vnc/oss/local, registered via permission.RegisterHelpDoc — config-only,
		// no command surface) all have a skills.Description and must be discoverable
		// here: the listing's job is "the model learns help(asset) exists", which is
		// just as true for a doc-only type as for one with an executor.
		for _, want := range []string{
			"ssh", "serial", "database", "redis", "k8s", "etcd", "mongodb", "kafka",
			"rdp", "vnc", "oss", "local",
		} {
			desc, ok := got[want]
			if !ok {
				t.Fatalf("expected %q to be included, got %v", want, got)
			}
			if desc == "" {
				t.Fatalf("expected non-empty description for %q", want)
			}
		}
	})

	t.Run("a type with no embedded SKILL.md is not included", func(t *testing.T) {
		// "bogus" is not a registered asset type and never will be, so it has no
		// internal/ai/skills entry and must not appear in the listing — the listing is
		// derived from the embedded SKILL.md set, not from the asset-type registry.
		// (vnc used to stand in here; it now has a doc-only SKILL.md — see
		// internal/ai/skills/vnc — so it moved to the "included" case above.)
		got := allAssetTypeSkills()
		if _, ok := got["bogus"]; ok {
			t.Fatalf("bogus is not a real asset type; must not be included, got %v", got)
		}
	})
}

// TestDefinedCustomTypeSummaries_ListErrorDegradesToEmpty locks that a listing failure
// degrades to "no custom types shown" rather than failing the whole prompt build — this
// is a best-effort discovery aid, not a required part of the system prompt.
func TestDefinedCustomTypeSummaries_ListErrorDegradesToEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_custom_type_repo.NewMockCustomTypeRepo(ctrl)
	repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db unavailable"))
	orig := custom_type_repo.CustomType()
	custom_type_repo.RegisterCustomType(repo)
	t.Cleanup(func() { custom_type_repo.RegisterCustomType(orig) })

	got := definedCustomTypeSummaries(context.Background())
	if len(got) != 0 {
		t.Fatalf("expected no custom types on a list error, got %v", got)
	}
}
