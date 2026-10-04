package extension

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestPageToolCallCancel pins what CancelExtensionTool promises a page: the
// context the named call runs under — the one CallExtensionTool hands to the
// policy gate and on to Plugin.CallTool, where canceling it interrupts the guest
// and its host IO — ends, and only that call's.
func TestPageToolCallCancel(t *testing.T) {
	Convey("Given two page tool calls in flight", t, func() {
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		first, endFirst, err := e.toolCalls.begin(context.Background(), "inv-1")
		So(err, ShouldBeNil)
		second, endSecond, err := e.toolCalls.begin(context.Background(), "inv-2")
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
			_, _, err := e.toolCalls.begin(context.Background(), "inv-1")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "inv-1")
			endFirst()
		})

		Convey("an ended call's id may be used again", func() {
			endFirst()
			_, endAgain, err := e.toolCalls.begin(context.Background(), "inv-1")
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
			_, _, err := e.toolCalls.begin(context.Background(), "inv-early")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "canceled")
		})

		Convey("other calls are unaffected", func() {
			_, end, err := e.toolCalls.begin(context.Background(), "inv-other")
			So(err, ShouldBeNil)
			end()
		})
	})
}
