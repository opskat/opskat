package opskat

import (
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// heapSink makes the test's allocations escape to the heap, as a call's do;
// heapKept is what a call leaves alive.
var heapSink, heapKept []byte

// forcedGCs counts the collections runtime.GC started.
func forcedGCs() uint64 {
	s := []metrics.Sample{{Name: "/gc/cycles/forced:gc-cycles"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

// guestCall stands for one guest call that allocates mib MiB and drops it.
func guestCall(h *callHeap, mib int) {
	heapSink = make([]byte, mib<<20)
	heapSink = nil
	h.afterCall()
}

func TestCallHeapCollectsAtTheCallBoundary(t *testing.T) {
	Convey("With the Go pacer off, so only the call boundary collects", t, func() {
		defer debug.SetGCPercent(debug.SetGCPercent(-1))
		runtime.GC()
		h := newCallHeap()
		guestCall(h, 16) // the first large call sets the baseline
		before := forcedGCs()

		Convey("a call that leaves a little garbage is not followed by a collection", func() {
			guestCall(h, 1)
			guestCall(h, 1)
			So(forcedGCs(), ShouldEqual, before)
		})

		Convey("a call that leaves a lot of garbage is, and the garbage is gone", func() {
			guestCall(h, 16)
			So(forcedGCs(), ShouldEqual, before+1)
			So(h.heapObjects(), ShouldBeLessThan, 16<<20)
		})

		Convey("a large heap kept alive across calls is collected once, not after every call", func() {
			heapKept = make([]byte, 16<<20)
			h.afterCall()
			So(forcedGCs(), ShouldEqual, before+1)
			guestCall(h, 1)
			guestCall(h, 1)
			So(forcedGCs(), ShouldEqual, before+1)
			heapKept = nil
		})
	})
}
