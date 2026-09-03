package opskat

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDispatch(t *testing.T) {
	Convey("dispatch", t, func() {
		// Reset global registries
		resetRegistries()

		Convey("execute_tool dispatches to registered tool handler", func() {
			RegisterTool("echo", func(ctx *ToolContext) (any, error) {
				var args struct{ Msg string }
				_ = json.Unmarshal(ctx.Args, &args)
				return map[string]string{"echo": args.Msg}, nil
			})

			input, _ := json.Marshal(map[string]any{
				"tool": "echo",
				"args": json.RawMessage(`{"msg":"hello"}`),
			})
			result, err := dispatch("execute_tool", input)
			So(err, ShouldBeNil)

			var out map[string]string
			_ = json.Unmarshal(result, &out)
			So(out["echo"], ShouldEqual, "hello")
		})

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

		Convey("check_policy dispatches to registered policy checker", func() {
			RegisterPolicy(func(tool string, args json.RawMessage) (string, string) {
				return "read", "bucket/file.txt"
			})

			input, _ := json.Marshal(map[string]any{
				"tool": "get_object",
				"args": json.RawMessage(`{}`),
			})
			result, err := dispatch("check_policy", input)
			So(err, ShouldBeNil)

			var out struct {
				Action   string `json:"action"`
				Resource string `json:"resource"`
			}
			_ = json.Unmarshal(result, &out)
			So(out.Action, ShouldEqual, "read")
			So(out.Resource, ShouldEqual, "bucket/file.txt")
		})

		Convey("unknown function returns error", func() {
			_, err := dispatch("unknown_fn", nil)
			So(err, ShouldNotBeNil)
		})

		Convey("unknown tool returns error", func() {
			_, err := dispatch("execute_tool", []byte(`{"tool":"nonexistent","args":{}}`))
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
