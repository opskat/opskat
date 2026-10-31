package extensiontest

import (
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// passPoint accepts its contributes key and attaches nothing.
type passPoint string

func (p passPoint) Key() string                                  { return string(p) }
func (passPoint) Check(extension.ExtensionView) error            { return nil }
func (passPoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }

// AcceptingContributions is a contribution registry that knows every describe()
// key and judges none of them: it lets a test load a fixture through the real
// extension.Manager without the host's contribution points (internal/extreg),
// which a test of pkg/extension cannot import. A test that means the host's
// verdict attaches the loaded extension through those points instead.
func AcceptingContributions() *extension.ContributionRegistry {
	return extension.NewContributionRegistry(
		passPoint(extwire.KeyAssetTypes), passPoint(extwire.KeyTools), passPoint(extwire.KeyPolicy),
		passPoint(extwire.KeySnippets), passPoint(extwire.KeyPages), passPoint(extwire.KeyDatabaseDrivers),
	)
}
