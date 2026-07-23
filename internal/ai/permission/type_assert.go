package permission

import (
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// CanonicalTypeFor 把一个类型名或协议别名规范成资产类型。
//
// 别名来自本包既有的 permissionTypes 注册表（type_registry.go 的 init）：
// ssh←exec、database←sql、mongodb←mongo。opsctl batch 的 `sql:2:SELECT 1` 前缀
// 与 batch_exec 条目的 type 字段都落在这张表上，因此"沿用旧写法"不需要任何兼容 shim。
//
// 注意 GrantToolCp（"cp"）也注册在同一张表里，它不是资产类型；调用方拿到的
// canonical 会是 "cp"，与任何 asset.Type 都不相等，于是断言正常失败——这正确。
func CanonicalTypeFor(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	handler, ok := permissionTypeFor(name)
	if !ok {
		return "", false
	}
	return handler.canonical, true
}

// CanonicalExecTypeFor resolves a canonical asset type or protocol alias only
// when that name belongs to an executable asset permission type. This is the
// shared parser contract for optional type assertions: operation faces such as
// cp and doc-only asset types are deliberately excluded.
func CanonicalExecTypeFor(name string) (string, bool) {
	canonical, ok := CanonicalTypeFor(name)
	if !ok || canonical == GrantToolCp {
		return "", false
	}
	return canonical, true
}

// AssertAssetType 校验调用方声明的类型与资产的真实类型是否一致。
//
// declared 为空 = 不声明 = 跳过校验（这是缺省形态，spec §4.6 的表格）。
// 声明了就必须对得上：派发永远由资产导出，这里只把"模型/人写错方言"从协议层的
// 服务端报错（读起来像基础设施故障）提前成一条点名双方类型的建模错误。
//
// 调用方必须把它放在权限检查之前——它无副作用，而 CheckForAsset 会弹审批对话框。
func AssertAssetType(asset *asset_entity.Asset, declared string) error {
	if declared == "" {
		return nil
	}
	canonical, ok := CanonicalTypeFor(declared)
	if !ok {
		return fmt.Errorf("unknown type %q; asset %q is type=%s — call help(asset=%q) for its command syntax",
			declared, asset.Name, asset.Type, asset.Name)
	}
	if canonical != asset.Type {
		return fmt.Errorf("asset %q is type=%s, but you passed type=%s — call help(asset=%q) for its command syntax",
			asset.Name, asset.Type, declared, asset.Name)
	}
	return nil
}
