package extension

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestToolCancellation(t *testing.T) {
	Convey("Given a new tool cancellation", t, func() {
		var hardStops atomic.Int32
		c := newToolCancellation(func() { hardStops.Add(1) })
		Convey("ShouldStop is initially false", func() {
			So(c.shouldStop(), ShouldBeFalse)
		})
		Convey("after cancel, ShouldStop is true at once and the hard stop waits out the grace", func() {
			c.cancel(50 * time.Millisecond)
			So(c.shouldStop(), ShouldBeTrue)
			So(hardStops.Load(), ShouldEqual, 0)
			So(eventually(func() bool { return hardStops.Load() == 1 }), ShouldBeTrue)
		})
		Convey("cancel is idempotent: the grace starts once and the hard stop runs once", func() {
			c.cancel(20 * time.Millisecond)
			c.cancel(20 * time.Millisecond)
			So(c.shouldStop(), ShouldBeTrue)
			So(eventually(func() bool { return hardStops.Load() == 1 }), ShouldBeTrue)
			time.Sleep(60 * time.Millisecond)
			So(hardStops.Load(), ShouldEqual, 1)
		})
		Convey("a call that returned within the grace is not hard-stopped", func() {
			c.cancel(30 * time.Millisecond)
			c.release()
			time.Sleep(80 * time.Millisecond)
			So(hardStops.Load(), ShouldEqual, 0)
		})
	})
}

// TestInvocationCancellationIsScoped pins the property the instance pool
// depends on: canceling one running tool call must not reach any other.
func TestInvocationCancellationIsScoped(t *testing.T) {
	Convey("Given two concurrent invocations of one plugin", t, func() {
		a := newInvocation("inv-a", newToolCancellation(func() {}))
		b := newInvocation("inv-b", newToolCancellation(func() {}))
		defer a.close()
		defer b.close()

		Convey("neither reports stop before cancellation", func() {
			So(a.shouldStop(), ShouldBeFalse)
			So(b.shouldStop(), ShouldBeFalse)
		})

		Convey("canceling one leaves the other running", func() {
			a.stop(time.Minute)
			So(a.shouldStop(), ShouldBeTrue)
			So(b.shouldStop(), ShouldBeFalse)
		})
	})

	Convey("An invocation with no cancellation never reports stop", t, func() {
		inv := newInvocation("inv-policy", nil)
		defer inv.close()
		So(inv.shouldStop(), ShouldBeFalse)
	})
}

// TestInvocationHandlesAreScoped pins the other half: handle IDs are private to
// the invocation that opened them, and start over for the next one.
func TestInvocationHandlesAreScoped(t *testing.T) {
	Convey("Given two invocations that each open a handle", t, func() {
		a := newInvocation("inv-a", nil)
		b := newInvocation("inv-b", nil)
		defer a.close()
		defer b.close()

		ra := &countingCloser{Reader: strings.NewReader("from-a")}
		rb := &countingCloser{Reader: strings.NewReader("from-b")}
		idA, err := a.io.Register(&IOResource{Reader: ra, Closer: ra})
		So(err, ShouldBeNil)
		idB, err := b.io.Register(&IOResource{Reader: rb, Closer: rb})
		So(err, ShouldBeNil)

		Convey("both get the same ID because the tables are separate", func() {
			So(idA, ShouldEqual, idB)
		})

		Convey("each reads only its own stream", func() {
			got, err := readHandle(a, idA, 16)
			So(err, ShouldBeNil)
			So(string(got), ShouldEqual, "from-a")
			got, err = readHandle(b, idB, 16)
			So(err, ShouldBeNil)
			So(string(got), ShouldEqual, "from-b")
		})

		Convey("closing an invocation closes what the guest left open", func() {
			a.close()
			So(ra.closed, ShouldEqual, 1)
			So(rb.closed, ShouldEqual, 0)
			_, err := readHandle(a, idA, 16)
			So(err, ShouldNotBeNil)
		})
	})
}

type countingCloser struct {
	io.Reader
	closed int
}

func (c *countingCloser) Close() error {
	c.closed++
	return nil
}
