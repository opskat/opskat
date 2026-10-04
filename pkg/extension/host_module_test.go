package extension

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestHostOpAssetGetConfig(t *testing.T) {
	Convey("asset.get_config only ever reads the invocation's own asset", t, func() {
		host := newRecordedHost()
		host.configs[7] = json.RawMessage(`{"own":true}`)
		host.configs[99] = json.RawMessage(`{"secret":"someone else's"}`)

		call := func(inv *invocation, params string) ([]byte, error) {
			req, err := json.Marshal(map[string]any{"op": "asset.get_config", "params": json.RawMessage(params)})
			So(err, ShouldBeNil)
			return hostCall(withInvocation(context.Background(), inv), host, req)
		}

		Convey("the config returned is the asset the host scoped the call to", func() {
			inv := newInvocation("t#1", nil).scopedTo(&AssetRef{ID: 7, Type: "fixture"})
			out, err := call(inv, `{}`)
			So(err, ShouldBeNil)
			So(string(out), ShouldEqual, `{"own":true}`)
		})

		Convey("a guest naming another asset id is refused, not served", func() {
			inv := newInvocation("t#2", nil).scopedTo(&AssetRef{ID: 7, Type: "fixture"})
			out, err := call(inv, `{"asset_id":99}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "asset_id")
			So(out, ShouldBeNil)
		})

		Convey("a call with no asset gets a clear error", func() {
			_, err := call(newInvocation("t#3", nil), `{"asset_id":99}`)
			So(err, ShouldNotBeNil)
			_, err = call(newInvocation("t#4", nil), `{}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
		})
	})
}
