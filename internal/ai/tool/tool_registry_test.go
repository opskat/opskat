package tool

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestExecimplBlankImportWiresExecutors locks the single line in tool_registry.go
// that makes the unified exec tool work in the real app:
//
//	_ "github.com/opskat/opskat/internal/ai/execimpl"
//
// execimpl.init() is what populates permission's executor table (via
// permission.RegisterExecutor); nothing else triggers it. execimpl's own
// coverage_test.go (package execimpl) CANNOT catch that blank import being
// deleted from tool_registry.go: as a test in package execimpl, its test
// binary imports the execimpl package directly and always runs execimpl.init()
// regardless of whether tool_registry.go still blank-imports it. So that test
// would keep passing even if desktop/opsctl — both of which reach exec only
// through package tool — silently lost every executor and "exec" started
// reporting every asset type as unsupported.
//
// This test lives in package tool specifically because that's the package
// whose blank import is under test: permission.ExecutorFor only returns ok
// here if tool_registry.go's blank import actually ran execimpl.init(). Do
// NOT move this into package execimpl — that would reintroduce exactly the
// blind spot this test exists to close.
func TestExecimplBlankImportWiresExecutors(t *testing.T) {
	for _, assetType := range []string{asset_entity.AssetTypeSSH, asset_entity.AssetTypeK8s} {
		if _, ok := permission.ExecutorFor(assetType); !ok {
			t.Errorf("permission.ExecutorFor(%q) not registered from package tool; "+
				"is the blank import of internal/ai/execimpl still present in tool_registry.go?",
				assetType)
		}
	}
}
