package policy

// CommandPolicy 命令权限策略
type CommandPolicy struct {
	AllowList []string `json:"allow_list"`       // 直接执行的命令规则
	DenyList  []string `json:"deny_list"`        // 始终拒绝的命令规则
	Groups    []string `json:"groups,omitempty"` // 引用的权限组 ID（内置组: "builtin:xxx", 用户组: "123"）
}

// IsEmpty 检查策略是否为空（无规则且无引用组）
func (p *CommandPolicy) IsEmpty() bool {
	return len(p.AllowList) == 0 && len(p.DenyList) == 0 && len(p.Groups) == 0
}

// DefaultCommandPolicy 返回默认命令权限策略（引用内置权限组）
func DefaultCommandPolicy() *CommandPolicy {
	return &CommandPolicy{
		Groups: []string{BuiltinLinuxReadOnly, BuiltinDangerousDeny},
	}
}

// QueryPolicy SQL 权限策略（database 类型资产使用）
type QueryPolicy struct {
	AllowTypes []string `json:"allow_types"`      // 允许的语句类型: SELECT, SHOW, DESCRIBE, EXPLAIN
	DenyTypes  []string `json:"deny_types"`       // 拒绝的语句类型: DROP TABLE, TRUNCATE, ...
	DenyFlags  []string `json:"deny_flags"`       // 拒绝的特征: no_where_delete, prepare, call
	Groups     []string `json:"groups,omitempty"` // 引用的权限组 ID
}

// IsEmpty 检查策略是否为空
func (p *QueryPolicy) IsEmpty() bool {
	return len(p.AllowTypes) == 0 && len(p.DenyTypes) == 0 && len(p.DenyFlags) == 0 && len(p.Groups) == 0
}

// DefaultQueryPolicy 返回默认 SQL 权限策略（引用内置权限组）
func DefaultQueryPolicy() *QueryPolicy {
	return &QueryPolicy{
		Groups: []string{BuiltinSQLReadOnly, BuiltinSQLDangerousDeny},
	}
}

// RedisPolicy Redis 权限策略
type RedisPolicy struct {
	AllowList []string `json:"allow_list"`       // 允许的命令模式
	DenyList  []string `json:"deny_list"`        // 拒绝的命令模式
	Groups    []string `json:"groups,omitempty"` // 引用的权限组 ID
}

// IsEmpty 检查策略是否为空
func (p *RedisPolicy) IsEmpty() bool {
	return len(p.AllowList) == 0 && len(p.DenyList) == 0 && len(p.Groups) == 0
}

// MongoPolicy MongoDB 权限策略
type MongoPolicy struct {
	AllowTypes []string `json:"allow_types,omitempty"` // 允许的操作类型: find, findOne, aggregate, ...
	DenyTypes  []string `json:"deny_types,omitempty"`  // 拒绝的操作类型: dropDatabase, dropCollection, ...
	Groups     []string `json:"groups,omitempty"`      // 引用的权限组 ID
}

// IsEmpty 检查策略是否为空
func (p *MongoPolicy) IsEmpty() bool {
	return len(p.AllowTypes) == 0 && len(p.DenyTypes) == 0 && len(p.Groups) == 0
}

// DefaultMongoPolicy 返回默认 MongoDB 权限策略（引用内置权限组）
func DefaultMongoPolicy() *MongoPolicy {
	return &MongoPolicy{Groups: []string{BuiltinMongoReadOnly, BuiltinMongoDangerousDeny}}
}

// KafkaPolicy Kafka 权限策略
type KafkaPolicy struct {
	AllowList []string `json:"allow_list"`       // 允许的 Kafka action/resource 模式
	DenyList  []string `json:"deny_list"`        // 拒绝的 Kafka action/resource 模式
	Groups    []string `json:"groups,omitempty"` // 引用的权限组 ID
}

// IsEmpty 检查策略是否为空
func (p *KafkaPolicy) IsEmpty() bool {
	return len(p.AllowList) == 0 && len(p.DenyList) == 0 && len(p.Groups) == 0
}

// DefaultKafkaPolicy 返回默认 Kafka 权限策略（引用内置权限组）
func DefaultKafkaPolicy() *KafkaPolicy {
	return &KafkaPolicy{
		Groups: []string{BuiltinKafkaMetadataReadOnly, BuiltinKafkaDangerousDeny},
	}
}

// K8sPolicy K8S 权限策略（k8s 类型资产使用，与命令策略结构相同）
type K8sPolicy struct {
	AllowList []string `json:"allow_list"`       // 允许执行的 kubectl 命令模式
	DenyList  []string `json:"deny_list"`        // 拒绝执行的 kubectl 命令模式
	Groups    []string `json:"groups,omitempty"` // 引用的权限组 ID
}

// IsEmpty 检查策略是否为空
func (p *K8sPolicy) IsEmpty() bool {
	return len(p.AllowList) == 0 && len(p.DenyList) == 0 && len(p.Groups) == 0
}

// DefaultK8sPolicy 返回默认 K8S 权限策略（引用内置权限组）
func DefaultK8sPolicy() *K8sPolicy {
	return &K8sPolicy{
		Groups: []string{BuiltinK8sReadOnly, BuiltinK8sDangerousDeny},
	}
}

// OSSPolicy OSS 对象存储权限策略（与 KafkaPolicy 同形）
type OSSPolicy struct {
	AllowList []string `json:"allow_list"`       // 允许的 OSS action/resource 模式
	DenyList  []string `json:"deny_list"`        // 拒绝的 OSS action/resource 模式
	Groups    []string `json:"groups,omitempty"` // 引用的权限组 ID
}

// IsEmpty 检查策略是否为空
func (p *OSSPolicy) IsEmpty() bool {
	return len(p.AllowList) == 0 && len(p.DenyList) == 0 && len(p.Groups) == 0
}

// DefaultOSSPolicy 返回默认 OSS 权限策略（引用内置权限组）
func DefaultOSSPolicy() *OSSPolicy {
	return &OSSPolicy{
		Groups: []string{BuiltinOSSReadOnly, BuiltinOSSDangerousDeny},
	}
}

// Holder 策略持有者接口，Asset 和 Group 均实现此接口
type Holder interface {
	GetCommandPolicy() (*CommandPolicy, error)
	GetQueryPolicy() (*QueryPolicy, error)
	GetRedisPolicy() (*RedisPolicy, error)
	GetMongoPolicy() (*MongoPolicy, error)
	GetKafkaPolicy() (*KafkaPolicy, error)
	GetK8sPolicy() (*K8sPolicy, error)
	GetEtcdPolicy() (*EtcdPolicy, error)
	GetOSSPolicy() (*OSSPolicy, error)
}

// DefaultRedisPolicy 返回默认 Redis 权限策略（引用内置权限组）
func DefaultRedisPolicy() *RedisPolicy {
	return &RedisPolicy{
		Groups: []string{BuiltinRedisReadOnly, BuiltinRedisDangerousDeny},
	}
}

// EtcdPolicy etcd 权限策略（复用 RedisPolicy 结构）
type EtcdPolicy = RedisPolicy

// DefaultEtcdPolicy 返回默认 etcd 权限策略（引用内置权限组）
func DefaultEtcdPolicy() *EtcdPolicy {
	return &EtcdPolicy{
		Groups: []string{BuiltinEtcdReadOnly, BuiltinEtcdDangerousDeny},
	}
}

// --- 内置权限组 ID 常量 ---

const (
	BuiltinLinuxReadOnly         = "builtin:linux-readonly"
	BuiltinK8sReadOnly           = "builtin:k8s-readonly"
	BuiltinK8sDangerousDeny      = "builtin:k8s-dangerous-deny"
	BuiltinDockerReadOnly        = "builtin:docker-readonly"
	BuiltinCpFullAccess          = "builtin:cp-full-access"
	BuiltinDangerousDeny         = "builtin:dangerous-deny"
	BuiltinSQLReadOnly           = "builtin:sql-readonly"
	BuiltinSQLDangerousDeny      = "builtin:sql-dangerous-deny"
	BuiltinRedisReadOnly         = "builtin:redis-readonly"
	BuiltinRedisDangerousDeny    = "builtin:redis-dangerous-deny"
	BuiltinMongoReadOnly         = "builtin:mongo-readonly"
	BuiltinMongoReadWrite        = "builtin:mongo-readwrite"
	BuiltinMongoDangerousDeny    = "builtin:mongo-dangerous-deny"
	BuiltinKafkaMetadataReadOnly = "builtin:kafka-metadata-readonly"
	BuiltinKafkaMessageRead      = "builtin:kafka-message-read"
	BuiltinKafkaSchemaReadOnly   = "builtin:kafka-schema-readonly"
	BuiltinKafkaConnectReadOnly  = "builtin:kafka-connect-readonly"
	BuiltinKafkaOperator         = "builtin:kafka-operator"
	BuiltinKafkaSecurityAdmin    = "builtin:kafka-security-admin"
	BuiltinKafkaDangerousDeny    = "builtin:kafka-dangerous-deny"
	// BuiltinKafkaMessageWriteDeny 是可选的，刻意不进 DefaultKafkaPolicy：默认未配置资产
	// 仍可通过审批 produce；用户选择本组后才会直接拒绝 message.write。
	BuiltinKafkaMessageWriteDeny = "builtin:kafka-message-write-deny"
	BuiltinEtcdReadOnly          = "builtin:etcd-readonly"
	BuiltinEtcdDangerousDeny     = "builtin:etcd-dangerous-deny"
	BuiltinOSSReadOnly           = "builtin:oss-readonly"
	// BuiltinOSSDangerousDeny 只含 object.presign.write *（见 D9）：预签名 PUT URL 是唯一
	// 把写权限完全移出本产品的操作 —— URL 签发后任何持有者都能在有效期内写入，不再经过
	// 任何策略/审批/审计。put/copy/move/delete 都走"每次都要过一次审批"的普通路径，
	// 因此不放入默认危险操作组。
	BuiltinOSSDangerousDeny = "builtin:oss-dangerous-deny"

	// BuiltinPrefix 内置权限组 ID 前缀
	BuiltinPrefix = "builtin:"
)
