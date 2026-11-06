package archtest

import (
	"strings"
	"testing"
)

// storeIndexForbiddenDeps 是商店索引包不得依赖的运行时：pkg/extstore 被扩展仓的发布工具与
// 宿主共用，只该带着 manifest 契约（pkg/extmanifest），不该把 WASM 运行时一并拖进来。
var storeIndexForbiddenDeps = []string{
	modulePath + "/pkg/extension",
	"github.com/tetratelabs/wazero",
}

// TestStoreIndexDoesNotDependOnRuntime 守护 pkg/extstore 不依赖扩展运行时 pkg/extension 与 wazero。
func TestStoreIndexDoesNotDependOnRuntime(t *testing.T) {
	deps := strings.Fields(runGo(t, repoRoot(t), nil, "list", "-deps", "./pkg/extstore/..."))
	for _, dep := range deps {
		for _, banned := range storeIndexForbiddenDeps {
			if dep == banned || strings.HasPrefix(dep, banned+"/") {
				t.Errorf("pkg/extstore 依赖了 %s：商店索引包不得依赖扩展运行时", dep)
			}
		}
	}
}
