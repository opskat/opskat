package opskat

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestToolRejectsInjectedAssetParameter(t *testing.T) {
	Convey("A tool may not declare the asset the host injects", t, func() {
		resetRegistries()
		// The exec target is what policy, approval and grant are all keyed on.
		// A second, argument-supplied asset id would let a call reach an asset the
		// user never granted, so it is refused where it is written rather than
		// silently ignored at call time.
		Tool("note_list", func(_ *ToolContext, _ struct {
			AssetID int64 `json:"asset_id"`
		}) (any, error) {
			return nil, nil
		}).Policy("read")
		shouldFailDescribe(`opskat: tool "note_list" declares parameter "asset_id" — the asset a tool runs against is the exec target, injected by the host; read ctx.Asset instead`)
	})
}

func TestToolHostOnly(t *testing.T) {
	type args struct {
		Q string `json:"q"`
	}
	noop := func(_ *ToolContext, _ args) (any, error) { return nil, nil }
	Convey("a host-only tool is reported with hostOnly and exclusive with the other kinds", t, func() {
		resetRegistries()
		AssetType[demoConfig]("demo")

		Tool("review", noop).HostOnly().Timeout(time.Minute)
		Tool("run", noop).Policy("read")
		_, err := dispatchDescribe()
		So(err, ShouldBeNil)
		byName := map[string]map[string]any{}
		for _, raw := range decodeContributes(t)["tools"].([]any) {
			tool := raw.(map[string]any)
			byName[tool["name"].(string)] = tool
		}
		So(byName["review"]["hostOnly"], ShouldEqual, true)
		So(byName["review"], ShouldNotContainKey, "policyAction")
		So(byName["review"], ShouldNotContainKey, "pageOnly")
		So(byName["review"]["timeoutMs"], ShouldEqual, 60000)
		So(byName["run"], ShouldNotContainKey, "hostOnly")

		resetRegistries()
		Tool("a", noop).Policy("read").HostOnly()
		shouldFailDescribe(`tool "a"`, "HostOnly is exclusive")
		resetRegistries()
		Tool("b", noop).HostOnly().Policy("read")
		shouldFailDescribe(`tool "b"`, "Policy is exclusive")
		resetRegistries()
		Tool("c", noop).PageOnly().HostOnly()
		shouldFailDescribe(`tool "c"`, "HostOnly is exclusive")
		resetRegistries()
		Tool("d", noop).HostOnly().PageOnly()
		shouldFailDescribe(`tool "d"`, "PageOnly is exclusive")
		resetRegistries()
		Tool("f", noop).HostOnly().PolicyFunc([]string{"read"}, func(args) (string, string) { return "read", "" })
		shouldFailDescribe(`tool "f"`, "PolicyFunc is exclusive")
		resetRegistries()
		Tool("g", noop).HostOnly().Timeout(11 * time.Minute)
		shouldFailDescribe(`tool "g"`, "outside [1ms, 10m0s]")
	})
	Convey("a host-only tool cannot declare no timeout, and a tool of no kind fails describe", t, func() {
		resetRegistries()
		AssetType[demoConfig]("demo")
		Tool("e", noop).HostOnly().NoTimeout()
		_, err := dispatchDescribe()
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, `"e"`)

		resetRegistries()
		AssetType[demoConfig]("demo")
		Tool("bare", noop)
		_, err = dispatchDescribe()
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "HostOnly")
	})
}
