// Package dbdriver 是数据库驱动的注册表：每个驱动注册一份实现（Driver）和一份描述
// （Descriptor），共享代码按存进资产配置的驱动 id 取用，不再按驱动名分支。
//
// 本包只依赖实体层，内置驱动是它的子包（mysql / postgresql / mssql / sqlite），在各自的
// init() 里 Register；新增驱动 = 新增一个注册，不改这里也不改调用方。
package dbdriver

import (
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// Driver 是一个数据库驱动在宿主侧的实现。
type Driver interface {
	// Descriptor 返回驱动描述：共享代码与前端据此决定名字、别名、表单字段与功能入口。
	Descriptor() Descriptor
	ConfigRules
}

// ConfigRules 是驱动对资产配置的全部规则：落库前校验，以及 opsctl / AI 创建、更新资产时
// 入参的必填、默认值与写入方式。
//
// 所有驱动共有的字段（driver、database、read_only、query_timeout_seconds、credential_id、
// 隧道与代理链）由数据库资产类型处理器统一处理，不在这里；口令也由它加密写入——驱动只需
// 在描述里声明自己用不用 FieldPassword。
type ConfigRules interface {
	// ValidateConfig 校验即将落库的配置里由驱动决定的部分。
	ValidateConfig(a *asset_entity.Asset, cfg *asset_entity.DatabaseConfig) error
	// CreateDefaults 返回创建时要补进入参的默认值（如默认端口）。它们在校验与审批之前
	// 写回入参，审批人看到的就是将要落库的值。
	CreateDefaults(args Args) map[string]any
	// ValidateCreateArgs 校验创建资产时驱动要求的必填入参。
	ValidateCreateArgs(args Args) error
	// ApplyCreateArgs 把驱动自有的连接入参写进新建资产的配置。
	ApplyCreateArgs(cfg *asset_entity.DatabaseConfig, args Args)
	// ApplyUpdateArgs 把本次更新携带的驱动自有入参合并进已有配置。调用时共有字段（含
	// ssh_asset_id）已经合并进 cfg。
	ApplyUpdateArgs(cfg *asset_entity.DatabaseConfig, args Args)
	// ConnectionView 返回驱动自有的非敏感连接字段，供 AI / opsctl 查看资产时展示。
	ConnectionView(cfg *asset_entity.DatabaseConfig) map[string]any
}

// Args 是 opsctl / AI 入参（一个 map[string]any）的只读视图。取值规则由数据库资产类型
// 处理器用共享的 Arg* 助手实现，驱动因此不必（也不能）自己再解析一遍原始 map。
type Args interface {
	// Has 报告入参里是否带了这个键（带了但值为空也算）。
	Has(key string) bool
	String(key string) string
	Int(key string) int
}
