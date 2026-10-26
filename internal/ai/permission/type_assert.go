package permission

import (
	"fmt"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// 驱动名（mysql、postgres、sqlite3 …）来自驱动注册表：每个驱动在描述里声明自己的 id 与
// 别名（dbdriver.Descriptor.Names），这里不维护第二份名单。
//
// 这些名字故意不注册进 permissionTypes：那张表是权限派发、审批标签（ApprovalTypeFor）
// 与 grant 支持（SupportsGrantApproval）的唯一真相源，而 "mysql" 三者都不是——塞进去会让
// SupportsGrantApproval("mysql") 报 true，把 grant 契约扩大到一个根本不是审批类型的词上。
//
// 语义上它们也不是 database 的同义词：`sql`/`db` 说的是同一个资产类型的另一个叫法，
// `mysql` 说的是 DatabaseConfig.Driver 的取值。若按普通别名注册（mysql→database 就完事），
// type=mysql 会在一个 PostgreSQL 资产上静默通过——而 --type 存在的唯一理由就是把
// "方言写错"提前变成错误，一个会撒谎的断言比没有断言更糟。所以驱动名多带一个约束，
// 由 AssertAssetType 落实。
//
// 两套名字必须无交集：resolveDeclaredType 先查 permissionTypes，撞名的驱动名会被悄悄
// 遮蔽、连带丢掉驱动约束。由 TestDriverNamesDoNotCollideWithPermissionTypes 把关。

// resolveDeclaredType 解析一个调用方声明的类型名，返回它断言的资产类型，以及——仅当
// 声明的是驱动名时——它额外断言的驱动。driver 为空表示"只断言类型，不涉及方言"。
func resolveDeclaredType(name string) (canonical string, driver asset_entity.DatabaseDriver, ok bool) {
	if name == "" {
		return "", "", false
	}
	if handler, found := permissionTypeFor(name); found {
		return handler.canonical, "", true
	}
	if d, found := dbregistry.ByName(name); found {
		return asset_entity.AssetTypeDatabase, d.Descriptor().ID, true
	}
	return "", "", false
}

// CanonicalTypeFor 把一个类型名、协议别名或驱动名规范成资产类型。
//
// 别名来自两处：本包既有的 permissionTypes 注册表（type_registry.go 的 init）——
// ssh←exec、database←sql/db、mongodb←mongo、k8s←kubernetes/kube；以及驱动注册表里
// 各驱动声明的名字——database←mysql/postgres/…。opsctl batch 的 `sql:2:SELECT 1` 前缀与
// batch_exec 条目的 type 字段都落在这里，因此"沿用旧写法"不需要任何兼容 shim，而
// `mysql:2:SELECT 1` 也与 --type mysql 认同一套词。
//
// 驱动名在这里只解析到 "database"：驱动约束不在类型解析这一层，它需要资产本身才能判定，
// 由 AssertAssetType 落实。想连驱动一起解析的调用方用 resolveDeclaredType。
//
// 注意 GrantToolCp（"cp"）也注册在 permissionTypes 里，它不是资产类型；调用方拿到的
// canonical 会是 "cp"，与任何 asset.Type 都不相等，于是断言正常失败——这正确。
func CanonicalTypeFor(name string) (string, bool) {
	canonical, _, ok := resolveDeclaredType(name)
	return canonical, ok
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
// 声明的若是驱动名（mysql/postgres/…，来自驱动注册表），断言多走一层：类型对上之后
// 还要求资产的 DatabaseConfig.Driver 一致。这正是允许驱动名当 --type 值的前提——
// 只把 mysql 映射到 database 就等于让 type=mysql 在 PostgreSQL 资产上通过，
// 恰好在它该拦住的地方失灵。
//
// 调用方必须把它放在权限检查之前——它无副作用，而 CheckForAsset 会弹审批对话框。
func AssertAssetType(asset *asset_entity.Asset, declared string) error {
	if declared == "" {
		return nil
	}
	canonical, driver, ok := resolveDeclaredType(declared)
	if !ok {
		return fmt.Errorf("unknown type %q; asset %q is type=%s — call help(asset=%q) for its command syntax",
			declared, asset.Name, asset.Type, asset.Name)
	}
	if canonical != asset.Type {
		return fmt.Errorf("asset %q is type=%s, but you passed type=%s — call help(asset=%q) for its command syntax",
			asset.Name, asset.Type, declared, asset.Name)
	}
	if driver == "" {
		return nil
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return err
	}
	if cfg.Driver != driver {
		return fmt.Errorf("asset %q is a database with driver=%s, but you passed type=%s — call help(asset=%q) for its command syntax",
			asset.Name, cfg.Driver, declared, asset.Name)
	}
	return nil
}
