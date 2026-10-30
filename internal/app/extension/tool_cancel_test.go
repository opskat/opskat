package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/pkg/extension"
)

// notRunningInPlugin is a Plugin.CancelTool that finds nothing running: the
// call has registered with the binder but not reached the runtime yet.
func notRunningInPlugin(string) bool { return false }

// TestPageToolCallCancelGoesThroughThePlugin: a call the runtime is running is
// canceled by its invocation id there — which raises ShouldStop and gives the
// guest a grace period — rather than by cutting its context off at once.
func TestPageToolCallCancelGoesThroughThePlugin(t *testing.T) {
	Convey("Given a page tool call the plugin is running", t, func() {
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		var canceled []string
		cancelTool := func(id string) bool { canceled = append(canceled, id); return true }
		ctx, end, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-1", cancelTool)
		So(err, ShouldBeNil)
		defer end()

		Convey("canceling it asks the plugin to cancel that invocation and leaves the context alone", func() {
			So(e.CancelExtensionTool("inv-1"), ShouldBeNil)
			So(canceled, ShouldResemble, []string{"inv-1"})
			So(ctx.Err(), ShouldBeNil)
		})

		Convey("it is a page call of its own extension only", func() {
			So(e.toolCalls.isPageCall("fixture-ext", "inv-1"), ShouldBeTrue)
			So(e.toolCalls.isPageCall("other-ext", "inv-1"), ShouldBeFalse)
			So(e.toolCalls.isPageCall("fixture-ext", "fixture-ext#1"), ShouldBeFalse)
			end()
			So(e.toolCalls.isPageCall("fixture-ext", "inv-1"), ShouldBeFalse)
		})
	})
}

// TestPageToolCallCancel pins what CancelExtensionTool promises a page whose
// call has not reached the plugin yet: the context the named call runs under —
// the one CallExtensionTool hands on to Plugin.CallTool — ends, and only that
// call's.
func TestPageToolCallCancel(t *testing.T) {
	Convey("Given two page tool calls in flight", t, func() {
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		first, endFirst, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-1", notRunningInPlugin)
		So(err, ShouldBeNil)
		second, endSecond, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-2", notRunningInPlugin)
		So(err, ShouldBeNil)
		defer endSecond()

		Convey("canceling one by invocation id ends that call's context only", func() {
			So(e.CancelExtensionTool("inv-1"), ShouldBeNil)
			So(first.Err(), ShouldEqual, context.Canceled)
			So(second.Err(), ShouldBeNil)

			Convey("and canceling it again once it has ended is not an error", func() {
				endFirst()
				So(e.CancelExtensionTool("inv-1"), ShouldBeNil)
			})
		})

		Convey("a second call under a running invocation id is refused", func() {
			_, _, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-1", notRunningInPlugin)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "inv-1")
			endFirst()
		})

		Convey("an ended call's id may be used again", func() {
			endFirst()
			_, endAgain, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-1", notRunningInPlugin)
			So(err, ShouldBeNil)
			endAgain()
		})

		Convey("canceling with no invocation id is refused", func() {
			So(e.CancelExtensionTool(""), ShouldNotBeNil)
			endFirst()
		})
	})
}

// Wails serves every IPC call on its own goroutine, so a page that aborts right
// after starting a call can have its CancelExtensionTool land before
// CallExtensionTool registers the call. The page has already been told the call
// was aborted; the call must not then run anyway (and, gated, pop an approval or
// write something the page believes it stopped).
func TestPageToolCallCanceledBeforeItStarts(t *testing.T) {
	Convey("Given a cancel that arrives before its call begins", t, func() {
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		So(e.CancelExtensionTool("inv-early"), ShouldBeNil)

		Convey("the call is refused instead of running", func() {
			_, _, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-early", notRunningInPlugin)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "canceled")
		})

		Convey("other calls are unaffected", func() {
			_, end, err := e.toolCalls.begin(context.Background(), "fixture-ext", "inv-other", notRunningInPlugin)
			So(err, ShouldBeNil)
			end()
		})
	})
}

// The runtime names the calls whose caller supplies no id — AI exec, opsctl —
// "<extension>#<n>". A page picks its own invocation ids, so one shaped like
// that could name a call the runtime started for someone else: canceling by it
// from the page must never reach that call, while the page's own ids still
// cancel the page's own calls.
func TestPageCannotCancelARuntimeNamedCall(t *testing.T) {
	e, assets, rec := pageCallFixtureWithEvents(t)
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(fixtureAsset(t, "http://127.0.0.1:9"), nil).AnyTimes()
	plugin := e.service.Manager().GetExtension("fixture-ext").Plugin

	aiCtx, stopAI := context.WithCancel(context.Background())
	defer stopAI()
	aiDone := make(chan error, 1)
	go func() {
		_, err := plugin.CallTool(aiCtx, extension.ToolCall{Name: "stream", Args: json.RawMessage(`{"count":100000000,"delay_ms":1}`)})
		aiDone <- err
	}()

	// A page call registered under a runtime-shaped id and canceled before it
	// reaches the plugin: the window in which the page's id is all that stands
	// between its cancel and Plugin.CancelTool. Every id the runtime may have
	// handed the AI call is tried, over and over until the call has surely
	// started.
	pageCancelsRuntimeIDs := func() {
		for n := 1; n <= 64; n++ {
			id := fmt.Sprintf("fixture-ext#%d", n)
			_, end, err := e.toolCalls.begin(context.Background(), "fixture-ext", id, plugin.CancelTool)
			_ = e.CancelExtensionTool(id)
			if err == nil {
				end()
			}
		}
	}
	require.Never(t, func() bool {
		pageCancelsRuntimeIDs()
		select {
		case err := <-aiDone:
			t.Logf("runtime-named call ended: %v", err)
			return true
		default:
			return false
		}
	}, time.Second, 20*time.Millisecond, "a page cancel reached a call the runtime named")

	t.Run("a page call may not take a runtime-shaped id", func(t *testing.T) {
		_, err := e.CallExtensionTool("fixture-ext", "stream", `{"count":1,"delay_ms":0}`, "fixture-ext#9999", 1)
		require.Error(t, err)
		require.Error(t, e.CancelExtensionTool("fixture-ext#9999"))
	})

	t.Run("a page's own id still cancels its own call", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			_, err := e.CallExtensionTool("fixture-ext", "stream", `{"count":100000000,"delay_ms":1}`, "inv-own", 1)
			done <- err
		}()
		require.Eventually(t, func() bool { return len(rec.forInvocation("inv-own")) > 0 }, 10*time.Second, 5*time.Millisecond)
		require.NoError(t, e.CancelExtensionTool("inv-own"))
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("canceled page call never returned")
		}
	})

	stopAI()
	select {
	case <-aiDone:
	case <-time.After(10 * time.Second):
		t.Fatal("runtime-named call never returned")
	}
}
