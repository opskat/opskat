package extension

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/assetconn"

	. "github.com/smartystreets/goconvey/convey"
)

type recordingHTTPCache struct{ invalidated []int64 }

func (r *recordingHTTPCache) InvalidateAssetHTTPClient(assetID int64) {
	r.invalidated = append(r.invalidated, assetID)
}

// An extension's name is its author's choice ("kafka", "ssh", "oss" all pass the
// manifest's name rule), and built-in protocols register their own pooled-
// connection invalidators in the same process-wide table. Loading such an
// extension must not silently replace a built-in's hook — that would leave, say,
// Kafka clients built for an asset's old credentials in use after the user
// changed them.
func TestRegisterHTTPCacheInvalidatorKeepsBuiltinHooks(t *testing.T) {
	Convey("Given a built-in protocol's invalidator and an extension with the same name", t, func() {
		var builtin []int64
		assetconn.RegisterInvalidator("kafka", func(_ context.Context, assetID int64) error {
			builtin = append(builtin, assetID)
			return nil
		})
		cache := &recordingHTTPCache{}
		RegisterHTTPCacheInvalidator("kafka", cache)
		t.Cleanup(func() {
			assetconn.UnregisterForTest("kafka")
			assetconn.UnregisterForTest(httpCacheInvalidatorName("kafka"))
		})

		Convey("invalidating an asset reaches both", func() {
			assetconn.InvalidateAsset(context.Background(), 7)
			So(builtin, ShouldResemble, []int64{7})
			So(cache.invalidated, ShouldResemble, []int64{7})
		})
	})
}
