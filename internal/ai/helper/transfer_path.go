package helper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ParseTransferEndpoint parses one side of a cp-style transfer path, shared by the AI cp
// tool and opsctl cp: "<asset>:<path>". <asset> is anything assetref.Resolve accepts (id
// or name); the remainder after the first colon is the path on that asset.
//
// A string with no colon, or with nothing before the colon (":foo"), is always local
// (asset is nil, path is the input unchanged) without any asset lookup. Otherwise the
// text after the colon must start with "/" to be read as a remote path -- that is what
// keeps Windows paths like "C:\windows" local (the D15 guard below still looks up the
// prefix to decide whether an error is warranted, but a miss leaves the path local).
//
// D15 guard: when the text after the colon does NOT start with "/" but the prefix
// genuinely resolves to an asset, that is reported as an error naming the correct
// "<asset>:/<path>" form instead of silently falling back to a local path -- a typo'd
// remote path must not turn into a confusing "file not found" against a literal path on
// disk.
func ParseTransferEndpoint(ctx context.Context, s string) (asset *asset_entity.Asset, path string, err error) {
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return nil, s, nil
	}
	prefix := s[:idx]
	rest := s[idx+1:]

	if strings.HasPrefix(rest, "/") {
		a, resolveErr := assetref.Resolve(ctx, prefix)
		if resolveErr != nil {
			return nil, "", fmt.Errorf("resolving asset %q: %w", prefix, resolveErr)
		}
		return a, rest, nil
	}

	// D15: only error when the prefix really is an asset; otherwise this is a local
	// path that happens to contain a colon (e.g. a Windows drive letter).
	if _, resolveErr := assetref.Resolve(ctx, prefix); resolveErr == nil {
		return nil, "", fmt.Errorf(
			"remote paths must be written %s:/<path> (missing leading \"/\" after the colon); got %q", prefix, s)
	}
	return nil, s, nil
}
