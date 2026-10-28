package snippet_svc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

var (
	snippetCatIDRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	snippetSeedKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// builtinCategoryIDs is the immutable set of category ids owned by the app core.
// Extensions may not re-declare these — they are reserved.
var builtinCategoryIDs = map[string]struct{}{
	"shell":  {},
	"sql":    {},
	"redis":  {},
	"mongo":  {},
	"prompt": {},
}

func isBuiltinCategoryID(id string) bool {
	_, ok := builtinCategoryIDs[id]
	return ok
}

// ContributionPoint is the snippet contribution point: the rules for the snippet
// categories and seed snippets an extension contributes (describe()'s snippets).
//
// Attaching is a no-op: the category registry reads the categories of the loaded
// extensions through its provider (RefreshFromExtensions), and seeds are synced and
// removed by the extension's install and uninstall (extension_svc).
func ContributionPoint() extension.ContributionPoint { return snippetsPoint{} }

type snippetsPoint struct{}

func (snippetsPoint) Key() string { return extwire.KeySnippets }

// Check validates the snippets block; both of its halves are optional. A category
// is shown for one of the extension's own asset types; a seed goes into a builtin
// category or one the extension declares.
func (snippetsPoint) Check(v extension.ExtensionView) error {
	raw := v.Contribution(extwire.KeySnippets)
	if raw == nil {
		return nil
	}
	var s extwire.Snippets
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("snippets: %w", err)
	}
	var assetTypes []struct {
		Type string `json:"type"`
	}
	if raw := v.Contribution(extwire.KeyAssetTypes); raw != nil {
		if err := json.Unmarshal(raw, &assetTypes); err != nil {
			return fmt.Errorf("assetTypes: %w", err)
		}
	}
	assetTypeSet := make(map[string]struct{}, len(assetTypes))
	for _, at := range assetTypes {
		assetTypeSet[at.Type] = struct{}{}
	}

	catIDs := make(map[string]struct{}, len(s.Categories))
	for i, c := range s.Categories {
		if c.ID == "" {
			return fmt.Errorf("snippets.categories[%d]: id is required", i)
		}
		if !snippetCatIDRe.MatchString(c.ID) {
			return fmt.Errorf("snippets.categories[%s]: id must match %s", c.ID, snippetCatIDRe.String())
		}
		if isBuiltinCategoryID(c.ID) {
			return fmt.Errorf("snippets.categories[%s]: id collides with a builtin category", c.ID)
		}
		if _, dup := catIDs[c.ID]; dup {
			return fmt.Errorf("snippets.categories[%s]: duplicate category id", c.ID)
		}
		catIDs[c.ID] = struct{}{}
		if c.AssetType == "" {
			return fmt.Errorf("snippets.categories[%s]: assetType is required", c.ID)
		}
		if _, ok := assetTypeSet[c.AssetType]; !ok {
			return fmt.Errorf("snippets.categories[%s]: assetType %q is not one of this extension's asset types", c.ID, c.AssetType)
		}
	}

	seedKeys := make(map[string]struct{}, len(s.Seed))
	for i, seed := range s.Seed {
		if seed.Key == "" {
			return fmt.Errorf("snippets.seed[%d]: key is required", i)
		}
		if !snippetSeedKeyRe.MatchString(seed.Key) {
			return fmt.Errorf("snippets.seed[%s]: key must match %s", seed.Key, snippetSeedKeyRe.String())
		}
		if _, dup := seedKeys[seed.Key]; dup {
			return fmt.Errorf("snippets.seed[%s]: duplicate seed key", seed.Key)
		}
		seedKeys[seed.Key] = struct{}{}
		if strings.TrimSpace(seed.Name) == "" {
			return fmt.Errorf("snippets.seed[%s]: name is required", seed.Key)
		}
		if strings.TrimSpace(seed.Content) == "" {
			return fmt.Errorf("snippets.seed[%s]: content is required", seed.Key)
		}
		if seed.Category == "" {
			return fmt.Errorf("snippets.seed[%s]: category is required", seed.Key)
		}
		_, isLocal := catIDs[seed.Category]
		if !isBuiltinCategoryID(seed.Category) && !isLocal {
			return fmt.Errorf("snippets.seed[%s]: category %q is neither builtin nor declared by this extension", seed.Key, seed.Category)
		}
	}
	return nil
}

func (snippetsPoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }
