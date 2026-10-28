package opskat

import (
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDispatch(t *testing.T) {
	Convey("dispatch", t, func() {
		resetRegistries()

		Convey("execute_tool runs a page-only tool like any other", func() {
			Tool("ping", func(ctx *ToolContext, _ struct{}) (any, error) {
				return map[string]string{"pong": "ok"}, nil
			}).PageOnly()

			result, err := dispatch("execute_tool", []byte(`{"tool":"ping","args":{}}`))
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

		Convey("test_connection dispatches to the asset type's declared handler with its decoded config", func() {
			type cfg struct {
				Host string `json:"host"`
			}
			var seen cfg
			AssetType[cfg]("notebook").TestConnection(func(c cfg) error {
				seen = c
				return nil
			})

			_, err := dispatch("test_connection", []byte(`{"assetType":"notebook","config":{"host":"10.0.0.1"}}`))
			So(err, ShouldBeNil)
			So(seen.Host, ShouldEqual, "10.0.0.1")
		})

		Convey("test_connection surfaces the handler's error", func() {
			type cfg struct{}
			AssetType[cfg]("notebook").TestConnection(func(cfg) error {
				return fmt.Errorf("connection refused")
			})

			_, err := dispatch("test_connection", []byte(`{"assetType":"notebook","config":{}}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "connection refused")
		})

		Convey("test_connection against an asset type with no declared handler fails", func() {
			type cfg struct{}
			AssetType[cfg]("notebook")

			_, err := dispatch("test_connection", []byte(`{"assetType":"notebook","config":{}}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "does not declare a test connection handler")
		})

		Convey("test_connection against an unknown asset type fails", func() {
			_, err := dispatch("test_connection", []byte(`{"assetType":"nope","config":{}}`))
			So(err, ShouldNotBeNil)
		})
	})
}

func TestToolContextShouldStop(t *testing.T) {
	Convey("A tool sees the caller's cancellation through ShouldStop", t, func() {
		resetRegistries()
		var captured bool
		Tool("cancel_test", func(ctx *ToolContext, _ struct{}) (any, error) {
			captured = ctx.ShouldStop()
			return nil, nil
		}).PageOnly().NoTimeout()

		Convey("false while the call is not canceled", func() {
			th := NewTestHost()
			defer th.Close()
			_, err := th.CallTool(Asset{}, "cancel_test", map[string]any{})
			So(err, ShouldBeNil)
			So(captured, ShouldBeFalse)
		})

		Convey("true once it is", func() {
			th := NewTestHost(WithToolCancel())
			defer th.Close()
			_, err := th.CallTool(Asset{}, "cancel_test", map[string]any{})
			So(err, ShouldBeNil)
			So(captured, ShouldBeTrue)
		})
	})
}
