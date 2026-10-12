package asset_entity_test

// 数据库配置里驱动自有的规则在驱动注册表里（它依赖本包，本包不能反过来导入它）。
// 本目录的 Validate 测试要用到内置驱动，所以由外部测试包把它们链接进测试二进制。
import (
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
)
