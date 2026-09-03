package opskat

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDispatch(t *testing.T) {
	Convey("dispatch", t, func() {
		resetRegistries()

		Convey("execute_action dispatches to registered action handler", func() {
			RegisterAction("ping", func(ctx *ActionContext) (any, error) {
				return map[string]string{"pong": "ok"}, nil
			})

			input, _ := json.Marshal(map[string]any{
				"action": "ping",
				"args":   json.RawMessage(`{}`),
			})
			result, err := dispatch("execute_action", input)
			So(err, ShouldBeNil)

			var out map[string]string
			_ = json.Unmarshal(result, &out)
			So(out["pong"], ShouldEqual, "ok")
		})

		Convey("unknown function returns error", func() {
			_, err := dispatch("unknown_fn", nil)
			So(err, ShouldNotBeNil)
		})

		Convey("unknown action returns error", func() {
			_, err := dispatch("execute_action", []byte(`{"action":"nonexistent","args":{}}`))
			So(err, ShouldNotBeNil)
		})
	})
}

func TestActionContextShouldStop(t *testing.T) {
	Convey("When action cancel is triggered", t, func() {
		th := NewTestHost(WithActionCancel())
		defer th.Close()

		var captured bool
		resetRegistries()
		RegisterAction("cancel_test", func(ctx *ActionContext) (any, error) {
			captured = ctx.ShouldStop()
			return nil, nil
		})

		_, err := th.CallAction("cancel_test", json.RawMessage("{}"), func(TestEvent) {})
		So(err, ShouldBeNil)
		So(captured, ShouldBeTrue)
	})
}
