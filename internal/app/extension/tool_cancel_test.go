package extension

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
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
