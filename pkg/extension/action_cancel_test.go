package extension

import (
	"io"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestActionCancellation(t *testing.T) {
	Convey("Given a new cancellation", t, func() {
		c := NewActionCancellation()
		Convey("ShouldStop is initially false", func() {
			So(c.ShouldStop(), ShouldBeFalse)
		})
		Convey("After Cancel, ShouldStop is true", func() {
			c.Cancel()
			So(c.ShouldStop(), ShouldBeTrue)
		})
		Convey("Cancel is idempotent", func() {
			c.Cancel()
			c.Cancel()
			So(c.ShouldStop(), ShouldBeTrue)
		})
	})
}

// TestInvocationCancellationIsScoped pins the property the instance pool
// depends on: canceling one running action must not reach any other. The flag
// used to live on the shared HostProvider, which was only safe while a plugin
// mutex serialized every call.
func TestInvocationCancellationIsScoped(t *testing.T) {
	Convey("Given two concurrent invocations of one plugin", t, func() {
		a := newInvocation("inv-a", NewActionCancellation())
		b := newInvocation("inv-b", NewActionCancellation())
		defer a.close()
		defer b.close()

		Convey("neither reports stop before cancellation", func() {
			So(a.shouldStop(), ShouldBeFalse)
			So(b.shouldStop(), ShouldBeFalse)
		})

		Convey("canceling one leaves the other running", func() {
			a.cancel.Cancel()
			So(a.shouldStop(), ShouldBeTrue)
			So(b.shouldStop(), ShouldBeFalse)
		})
	})

	Convey("An invocation with no cancellation never reports stop", t, func() {
		inv := newInvocation("inv-tool", nil)
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
