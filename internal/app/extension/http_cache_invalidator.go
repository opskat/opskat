package extension

import (
	"context"

	"github.com/opskat/opskat/internal/assetconn"
)

// AssetHTTPCacheInvalidator is the part of a loaded extension's host provider
// (extension.DefaultHostProvider) that drops an asset's cached HTTP client.
type AssetHTTPCacheInvalidator interface {
	InvalidateAssetHTTPClient(assetID int64)
}

// RegisterHTTPCacheInvalidator hooks extName's cached HTTP clients into
// internal/assetconn, so an asset's client is dropped once its stored config
// changes or it is deleted, instead of being reused on the next open. A later
// registration for the same extension (a reload) replaces the earlier one.
func RegisterHTTPCacheInvalidator(extName string, provider AssetHTTPCacheInvalidator) {
	assetconn.RegisterInvalidator(httpCacheInvalidatorName(extName), func(_ context.Context, assetID int64) error {
		provider.InvalidateAssetHTTPClient(assetID)
		return nil
	})
}

// httpCacheInvalidatorName is extName's key in the assetconn table. The table is
// shared with built-in protocols ("ssh", "kafka", …), and an extension may carry
// any of those names; the namespace keeps it from replacing their hooks. ':' is
// outside the manifest name alphabet, so no extension name can reach it.
func httpCacheInvalidatorName(extName string) string { return "extension:" + extName }
