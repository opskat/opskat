package opskat

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestToolRejectsInjectedAssetParameter(t *testing.T) {
	Convey("A tool may not declare the asset the host injects", t, func() {
		resetRegistries()
		// The exec target is what policy, approval and grant are all keyed on.
		// A second, argument-supplied asset id would let a call reach an asset the
		// user never granted, so it is refused where it is written rather than
		// silently ignored at call time.
		So(func() {
			Tool("note_list", func(_ *ToolContext, _ struct {
				AssetID int64 `json:"asset_id"`
			}) (any, error) {
				return nil, nil
			})
		}, ShouldPanicWith, `opskat: tool "note_list" declares parameter "asset_id" — the asset a tool runs against is the exec target, injected by the host; read ctx.Asset instead`)
	})
}
