package opskat

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestToolContextSettings(t *testing.T) {
	Convey("ctx.Settings reads the extension's settings as a call without an asset sees them", t, func() {
		resetRegistries()
		Tool("read_settings", func(ctx *ToolContext, _ struct{}) (any, error) {
			return ctx.Settings()
		}).HostOnly()

		decode := func(result any) SettingsView {
			b, err := json.Marshal(result)
			So(err, ShouldBeNil)
			var view SettingsView
			So(json.Unmarshal(b, &view), ShouldBeNil)
			return view
		}

		Convey("non-secret values, and for each secret only whether it is set", func() {
			th := NewTestHost(WithSettings(map[string]any{"model": "fast", "threshold": 0.8}, map[string]bool{"apiKey": true, "spare": false}))
			defer th.Close()

			result, err := th.CallTool(Asset{}, "read_settings", struct{}{})
			So(err, ShouldBeNil)
			view := decode(result)
			So(view.Values, ShouldResemble, map[string]any{"model": "fast", "threshold": 0.8})
			So(view.Secrets, ShouldResemble, map[string]bool{"apiKey": true, "spare": false})
		})

		Convey("an extension that saved no settings reads an empty view", func() {
			th := NewTestHost()
			defer th.Close()

			result, err := th.CallTool(Asset{}, "read_settings", struct{}{})
			So(err, ShouldBeNil)
			view := decode(result)
			So(view.Values, ShouldBeEmpty)
			So(view.Secrets, ShouldBeEmpty)
		})

		Convey("a call scoped to an asset reads no settings, as on the host", func() {
			th := NewTestHost(WithSettings(map[string]any{"model": "fast"}, nil))
			defer th.Close()

			_, err := th.CallTool(Asset{ID: 3, Type: "x"}, "read_settings", struct{}{})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "asset")
		})
	})
}
