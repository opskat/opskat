package archtest

import (
	"strings"
	"testing"
)

// runtimeForbiddenDeps 是扩展运行时 pkg/extension 不得依赖的包：数据库驱动契约与共享线格式
// 属于贡献（由 internal/extreg 接入），运行时只认识 WASM 沙箱、宿主函数、权限与调用作用域。
var runtimeForbiddenDeps = []string{
	modulePath + "/pkg/dbdriver",
	modulePath + "/pkg/extwire",
}

// runtimeAllowedInternal 是运行时可以依赖的 internal/ 包（含子包）：基础设施（internal/pkg/*）与
// 构建版本信息，不是业务包。
var runtimeAllowedInternal = []string{
	modulePath + "/internal/pkg",
	modulePath + "/internal/buildinfo",
}

// TestExtensionRuntimeStaysContributionAgnostic 守护 pkg/extension 不依赖数据库驱动契约、
// 共享线格式与任何 internal/ 业务包。
func TestExtensionRuntimeStaysContributionAgnostic(t *testing.T) {
	deps := strings.Fields(runGo(t, repoRoot(t), nil, "list", "-deps", "./pkg/extension"))
	for _, dep := range deps {
		for _, banned := range runtimeForbiddenDeps {
			if dep == banned || strings.HasPrefix(dep, banned+"/") {
				t.Errorf("pkg/extension 依赖了 %s：运行时不得依赖数据库驱动契约与共享线格式", dep)
			}
		}
		if strings.HasPrefix(dep, modulePath+"/internal/") && !allowedRuntimeInternal(dep) {
			t.Errorf("pkg/extension 依赖了 %s：运行时不得依赖 internal/ 业务包", dep)
		}
	}
}

func allowedRuntimeInternal(dep string) bool {
	for _, allowed := range runtimeAllowedInternal {
		if dep == allowed || strings.HasPrefix(dep, allowed+"/") {
			return true
		}
	}
	return false
}
