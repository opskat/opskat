package asset_entity

// DatabaseDriverConfigValidator 校验数据库资产配置里由驱动决定的部分（必填字段、文件路径
// 规则等）。驱动无关的规则（查询超时范围、代理链）留在 validateDatabase。
type DatabaseDriverConfigValidator func(a *Asset, cfg *DatabaseConfig) error

// 各驱动的规则在驱动注册表（internal/dbdriver）里，而注册表依赖本包，只能反向接入；
// 与 RegisterConfigValidator 是同一个做法。
var databaseDriverConfigValidator DatabaseDriverConfigValidator

// SetDatabaseDriverConfigValidator 接入按驱动校验数据库配置的实现。只由驱动注册表在
// init() 里调用一次。
func SetDatabaseDriverConfigValidator(v DatabaseDriverConfigValidator) {
	databaseDriverConfigValidator = v
}
