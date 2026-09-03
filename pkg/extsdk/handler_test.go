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

		Convey("execute_tool hands the handler the asset the host named", func() {
			var seen Asset
			Tool("who", func(ctx *ToolContext, _ struct{}) (any, error) {
				seen = ctx.Asset
				return map[string]string{"ok": "1"}, nil
			}).Policy("read")

			_, err := dispatch("execute_tool", []byte(`{"tool":"who","args":{},"asset":{"id":12,"name":"prod notes","type":"notebook"}}`))
			So(err, ShouldBeNil)
			So(seen, ShouldResemble, Asset{ID: 12, Name: "prod notes", Type: "notebook"})
		})

		Convey("execute_action hands the handler the asset the host named", func() {
			var seen Asset
			RegisterAction("who", func(ctx *ActionContext) (any, error) {
				seen = ctx.Asset
				return nil, nil
			})

			_, err := dispatch("execute_action", []byte(`{"action":"who","args":{},"asset":{"id":3,"name":"n","type":"notebook"}}`))
			So(err, ShouldBeNil)
			So(seen, ShouldResemble, Asset{ID: 3, Name: "n", Type: "notebook"})
		})

		Convey("a call the host did not scope to an asset cannot read a config", func() {
			var configErr error
			Tool("cfg", func(ctx *ToolContext, _ struct{}) (any, error) {
				_, configErr = ctx.AssetConfig()
				return map[string]string{"ok": "1"}, nil
			}).Policy("read")

			th := NewTestHost()
			defer th.Close()

			_, err := dispatch("execute_tool", []byte(`{"tool":"cfg","args":{}}`))
			So(err, ShouldBeNil)
			So(configErr, ShouldNotBeNil)
			So(configErr.Error(), ShouldContainSubstring, "not scoped to an asset")
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

		_, err := th.CallAction(Asset{}, "cancel_test", json.RawMessage("{}"), func(TestEvent) {})
		So(err, ShouldBeNil)
		So(captured, ShouldBeTrue)
	})
}
