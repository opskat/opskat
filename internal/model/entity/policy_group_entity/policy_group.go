package policy_group_entity

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/opskat/opskat/internal/model/entity/policy"
)

// 策略类型常量
const (
	PolicyTypeCommand = policy.PolicyKindCommand
	PolicyTypeQuery   = policy.PolicyKindQuery
	PolicyTypeRedis   = policy.PolicyKindRedis
	PolicyTypeMongo   = policy.PolicyKindMongo
	PolicyTypeKafka   = policy.PolicyKindKafka
	PolicyTypeEtcd    = policy.PolicyKindEtcd
)

// PolicyGroup 权限组实体（数据库）
type PolicyGroup struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"column:name;type:varchar(255);not null" json:"name"`
	Description string `gorm:"column:description;type:text" json:"description"`
	PolicyType  string `gorm:"column:policy_type;type:varchar(50);not null" json:"policyType"`
	Policy      string `gorm:"column:policy;type:text;not null" json:"policy"`
	Createtime  int64  `gorm:"column:createtime" json:"createtime"`
	Updatetime  int64  `gorm:"column:updatetime" json:"updatetime"`

	// 非数据库字段，仅内置组/扩展组使用
	BuiltinID     string `gorm:"-" json:"-"`
	ExtensionName string `gorm:"-" json:"-"` // 扩展名称（如 "oss"）
}

// TableName GORM 表名
func (PolicyGroup) TableName() string {
	return "policy_groups"
}

// Validate 校验
func (pg *PolicyGroup) Validate() error {
	if pg.Name == "" {
		return errors.New("权限组名称不能为空")
	}
	if !isBuiltinKind(pg.PolicyType) && !hasExtensionPolicyType(pg.PolicyType) {
		return errors.New("无效的策略类型")
	}
	return nil
}

// PolicyGroupItem 返回给前端的权限组项
type PolicyGroupItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	PolicyType    string `json:"policyType"`
	Policy        string `json:"policy"`
	Builtin       bool   `json:"builtin"`
	ExtensionName string `json:"extensionName,omitempty"`
	Createtime    int64  `json:"createtime"`
	Updatetime    int64  `json:"updatetime"`
}

// ToItem 转为 PolicyGroupItem
func (pg *PolicyGroup) ToItem() *PolicyGroupItem {
	item := &PolicyGroupItem{
		Name:          pg.Name,
		Description:   pg.Description,
		PolicyType:    pg.PolicyType,
		Policy:        pg.Policy,
		ExtensionName: pg.ExtensionName,
		Createtime:    pg.Createtime,
		Updatetime:    pg.Updatetime,
	}
	if pg.BuiltinID != "" {
		item.ID = pg.BuiltinID
		item.Builtin = true
	} else {
		item.ID = strconv.FormatInt(pg.ID, 10)
	}
	return item
}

// --- 内置权限组 ---

func mustMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// builtinKindOrder 决定 BuiltinGroups() 的拼装顺序,按 registerBuiltinGroups 首次注册的
// 顺序派生(init 调用顺序即历史顺序 command→query→redis→mongo→kafka→etcd),不再手维护 ——
// 新增 kind 只需 registerBuiltinGroups 一处,自动入序,避免漏改顺序表导致内置组被静默丢弃。
var builtinKindOrder []string

// builtinGroupsByKind 每个 policyKind 贡献的内置权限组(纯数据注册表)。
// 新增策略 kind 时,在此处加一段 registerBuiltinGroups 即可,Validate / BuiltinGroups 自动覆盖。
var builtinGroupsByKind = map[string][]*PolicyGroup{}

func registerBuiltinGroups(kind string, groups ...*PolicyGroup) {
	if _, seen := builtinGroupsByKind[kind]; !seen {
		builtinKindOrder = append(builtinKindOrder, kind)
	}
	builtinGroupsByKind[kind] = append(builtinGroupsByKind[kind], groups...)
}

func init() {
	registerBuiltinGroups(PolicyTypeCommand,
		&PolicyGroup{
			BuiltinID:   policy.BuiltinLinuxReadOnly,
			Name:        "Linux Read-Only",
			Description: "Common Linux read-only commands",
			PolicyType:  PolicyTypeCommand,
			Policy: mustMarshal(&policy.CommandPolicy{
				AllowList: []string{
					"ls *", "cat *", "head *", "tail *",
					"grep *", "find *", "pwd", "wc *",
					"whoami", "hostname", "uname *", "id", "date",
					"env", "printenv *", "which *", "file *", "stat *",
					"df *", "du *", "free *", "uptime",
					"ps *", "top -b -n 1 *",
					"netstat *", "ss *", "ip *", "ifconfig *",
					"mount", "lsblk *", "blkid *",
					"lsof *", "vmstat *", "iostat *",
					"systemctl status *", "journalctl *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinK8sReadOnly,
			Name:        "Kubernetes Read-Only",
			Description: "Kubernetes read-only commands",
			PolicyType:  PolicyTypeCommand,
			Policy: mustMarshal(&policy.CommandPolicy{
				AllowList: []string{
					"kubectl get *", "kubectl describe *", "kubectl logs *",
					"kubectl top *", "kubectl explain *",
					"kubectl api-resources *", "kubectl api-versions",
					"kubectl cluster-info *", "kubectl config view *",
					"kubectl config get-contexts *", "kubectl version *",
					"kubectl auth can-i *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinK8sDangerousDeny,
			Name:        "Kubernetes Dangerous Deny",
			Description: "Deny dangerous Kubernetes commands",
			PolicyType:  PolicyTypeCommand,
			Policy: mustMarshal(&policy.CommandPolicy{
				DenyList: []string{
					"kubectl delete *",
					"kubectl replace --force *",
					"kubectl drain *",
					"kubectl debug *",
					"kubectl drain --force *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinDockerReadOnly,
			Name:        "Docker Read-Only",
			Description: "Docker read-only commands",
			PolicyType:  PolicyTypeCommand,
			Policy: mustMarshal(&policy.CommandPolicy{
				AllowList: []string{
					"docker ps *", "docker images *", "docker logs *",
					"docker inspect *", "docker stats *", "docker top *",
					"docker port *", "docker diff *", "docker history *",
					"docker info", "docker version",
					"docker network ls *", "docker network inspect *",
					"docker volume ls *", "docker volume inspect *",
					"docker compose ps *", "docker compose logs *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinDangerousDeny,
			Name:        "Dangerous Command Deny",
			Description: "Deny dangerous system commands",
			PolicyType:  PolicyTypeCommand,
			Policy: mustMarshal(&policy.CommandPolicy{
				DenyList: []string{
					"rm -rf /*",
					"mkfs *",
					"dd *",
					"shutdown *",
					"reboot *",
					"poweroff *",
					"halt *",
				},
			}),
		},
	)
	registerBuiltinGroups(PolicyTypeQuery,
		&PolicyGroup{
			BuiltinID:   policy.BuiltinSQLReadOnly,
			Name:        "SQL Read-Only",
			Description: "Allow query-only SQL statements",
			PolicyType:  PolicyTypeQuery,
			Policy: mustMarshal(&policy.QueryPolicy{
				AllowTypes: []string{
					"SELECT", "SHOW", "DESCRIBE", "EXPLAIN", "USE",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinSQLDangerousDeny,
			Name:        "SQL Dangerous Deny",
			Description: "Deny dangerous SQL operations",
			PolicyType:  PolicyTypeQuery,
			Policy: mustMarshal(&policy.QueryPolicy{
				DenyTypes: []string{
					"DROP TABLE", "DROP DATABASE", "TRUNCATE",
					"GRANT", "REVOKE",
					"CREATE USER", "DROP USER", "ALTER USER",
				},
				DenyFlags: []string{
					"no_where_delete",
					"no_where_update",
					"prepare",
				},
			}),
		},
	)
	registerBuiltinGroups(PolicyTypeRedis,
		&PolicyGroup{
			BuiltinID:   policy.BuiltinRedisReadOnly,
			Name:        "Redis Read-Only",
			Description: "Allow Redis read-only commands",
			PolicyType:  PolicyTypeRedis,
			Policy: mustMarshal(&policy.RedisPolicy{
				AllowList: []string{
					"GET", "MGET", "STRLEN",
					"HGET", "HGETALL", "HKEYS", "HVALS", "HLEN", "HMGET", "HEXISTS",
					"LRANGE", "LLEN", "LINDEX",
					"SMEMBERS", "SCARD", "SISMEMBER",
					"ZRANGE", "ZCARD", "ZSCORE", "ZRANK", "ZCOUNT",
					"TYPE", "TTL", "PTTL", "EXISTS", "DBSIZE", "KEYS", "SCAN",
					"INFO", "PING",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinRedisDangerousDeny,
			Name:        "Redis Dangerous Deny",
			Description: "Deny dangerous Redis commands",
			PolicyType:  PolicyTypeRedis,
			Policy: mustMarshal(&policy.RedisPolicy{
				DenyList: []string{
					"FLUSHDB", "FLUSHALL",
					"CONFIG SET *", "CONFIG RESETSTAT",
					"DEBUG *", "SHUTDOWN *",
					"SLAVEOF *", "REPLICAOF *",
					"ACL DELUSER *", "ACL SETUSER *",
					"SCRIPT FLUSH", "CLUSTER RESET *",
				},
			}),
		},
	)
	registerBuiltinGroups(PolicyTypeMongo,
		&PolicyGroup{
			BuiltinID:   policy.BuiltinMongoReadOnly,
			Name:        "MongoDB Read-Only",
			Description: "Allow MongoDB read-only operations",
			PolicyType:  PolicyTypeMongo,
			Policy: mustMarshal(&policy.MongoPolicy{
				AllowTypes: []string{
					"find", "findOne", "aggregate", "countDocuments",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinMongoReadWrite,
			Name:        "MongoDB Read-Write",
			Description: "Allow MongoDB CRUD operations",
			PolicyType:  PolicyTypeMongo,
			Policy: mustMarshal(&policy.MongoPolicy{
				AllowTypes: []string{
					"find", "findOne", "aggregate", "countDocuments",
					"insertOne", "insertMany",
					"updateOne", "updateMany",
					"deleteOne", "deleteMany",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinMongoDangerousDeny,
			Name:        "MongoDB Dangerous Deny",
			Description: "Deny dangerous MongoDB operations",
			PolicyType:  PolicyTypeMongo,
			Policy: mustMarshal(&policy.MongoPolicy{
				DenyTypes: []string{
					"dropDatabase", "dropCollection",
				},
			}),
		},
	)
	registerBuiltinGroups(PolicyTypeKafka,
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaMetadataReadOnly,
			Name:        "Kafka Metadata Read-Only",
			Description: "Allow Kafka cluster, broker, topic, and consumer group metadata reads",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				AllowList: []string{
					"cluster.read *",
					"broker.read *",
					"cluster.config.read *",
					"topic.list *",
					"topic.read *",
					"topic.config.read *",
					"consumer_group.list *",
					"consumer_group.read *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaMessageRead,
			Name:        "Kafka Message Read",
			Description: "Allow bounded Kafka message browsing",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				AllowList: []string{
					"message.read *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaSchemaReadOnly,
			Name:        "Kafka Schema Registry Read-Only",
			Description: "Allow Schema Registry read operations",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				AllowList: []string{
					"schema.read *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaConnectReadOnly,
			Name:        "Kafka Connect Read-Only",
			Description: "Allow Kafka Connect read operations",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				AllowList: []string{
					"connect.read *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaOperator,
			Name:        "Kafka Operator",
			Description: "Allow Kafka topic, message, and consumer group administration",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				AllowList: []string{
					"topic.create *",
					"topic.config.write *",
					"topic.partitions.write *",
					"topic.records.delete *",
					"message.write *",
					"consumer_group.offset.write *",
					"consumer_group.delete *",
					"schema.write *",
					"schema.delete *",
					"connect.write *",
					"connect.state.write *",
					"connect.delete *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaSecurityAdmin,
			Name:        "Kafka Security Admin",
			Description: "Allow Kafka ACL changes",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				AllowList: []string{
					"acl.read *",
					"acl.write *",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinKafkaDangerousDeny,
			Name:        "Kafka Dangerous Deny",
			Description: "Deny destructive and high-risk Kafka operations",
			PolicyType:  PolicyTypeKafka,
			Policy: mustMarshal(&policy.KafkaPolicy{
				DenyList: []string{
					"topic.delete *",
					"topic.records.delete *",
					"consumer_group.offset.write *",
					"consumer_group.delete *",
					"acl.write *",
					"schema.delete *",
					"connect.delete *",
				},
			}),
		},
	)
	registerBuiltinGroups(PolicyTypeEtcd,
		&PolicyGroup{
			BuiltinID:   policy.BuiltinEtcdReadOnly,
			Name:        "etcd Read-Only",
			Description: "Allow etcd read-only operations",
			PolicyType:  PolicyTypeEtcd,
			Policy: mustMarshal(&policy.EtcdPolicy{
				AllowList: []string{
					"get *",
					"endpoint *",
					"member list",
					"lease list",
				},
			}),
		},
		&PolicyGroup{
			BuiltinID:   policy.BuiltinEtcdDangerousDeny,
			Name:        "etcd Dangerous Deny",
			Description: "Deny dangerous etcd operations",
			PolicyType:  PolicyTypeEtcd,
			Policy: mustMarshal(&policy.EtcdPolicy{
				DenyList: []string{
					"auth enable", "auth disable",
					"user add *", "user delete *", "user passwd *",
					"role add *", "role delete *", "role grant-permission *", "role revoke-permission *",
					"member add *", "member remove *", "member update *",
					"move-leader *",
					"defrag",
					"compact *",
					"alarm disarm *",
					"snapshot save *",
				},
			}),
		},
	)
}

// builtinMap 内置组缓存
var builtinMap map[string]*PolicyGroup

func init() {
	builtinMap = make(map[string]*PolicyGroup)
	for _, pg := range BuiltinGroups() {
		builtinMap[pg.BuiltinID] = pg
	}
}

// BuiltinGroups 返回所有内置权限组(按 kind 顺序拼装)
func BuiltinGroups() []*PolicyGroup {
	groups := make([]*PolicyGroup, 0, len(builtinMap))
	for _, kind := range builtinKindOrder {
		groups = append(groups, builtinGroupsByKind[kind]...)
	}
	return groups
}

// isBuiltinKind 判断 policyType 是否为已注册内置 kind(合法 kind 从注册数据派生,替代 Validate 的 switch)。
func isBuiltinKind(kind string) bool {
	_, ok := builtinGroupsByKind[kind]
	return ok
}

// FindBuiltin 按 ID 查找内置权限组
func FindBuiltin(id string) *PolicyGroup {
	return builtinMap[id]
}

// IsBuiltinID 检查 ID 是否为内置权限组
func IsBuiltinID(id string) bool {
	return strings.HasPrefix(id, policy.BuiltinPrefix)
}

const ExtensionPrefix = "ext:"

var (
	extensionGroupMu  sync.RWMutex
	extensionGroupMap = make(map[string]*PolicyGroup)
)

// IsExtensionID returns true if the ID has the ext: prefix.
func IsExtensionID(id string) bool {
	return strings.HasPrefix(id, ExtensionPrefix)
}

// RegisterExtensionGroup registers an extension-provided policy group.
func RegisterExtensionGroup(pg *PolicyGroup) {
	extensionGroupMu.Lock()
	defer extensionGroupMu.Unlock()
	extensionGroupMap[pg.BuiltinID] = pg
}

// FindExtensionGroup looks up an extension policy group by ID.
func FindExtensionGroup(id string) *PolicyGroup {
	extensionGroupMu.RLock()
	defer extensionGroupMu.RUnlock()
	return extensionGroupMap[id]
}

// hasExtensionPolicyType checks if any extension group uses the given policy type.
func hasExtensionPolicyType(policyType string) bool {
	extensionGroupMu.RLock()
	defer extensionGroupMu.RUnlock()
	for _, pg := range extensionGroupMap {
		if pg.PolicyType == policyType {
			return true
		}
	}
	return false
}

// ExtensionGroups returns all registered extension policy groups.
func ExtensionGroups() []*PolicyGroup {
	extensionGroupMu.RLock()
	defer extensionGroupMu.RUnlock()
	groups := make([]*PolicyGroup, 0, len(extensionGroupMap))
	for _, pg := range extensionGroupMap {
		groups = append(groups, pg)
	}
	return groups
}

// UnregisterExtensionGroups removes all extension groups for a given policy type.
func UnregisterExtensionGroups(policyType string) {
	extensionGroupMu.Lock()
	defer extensionGroupMu.Unlock()
	for id, pg := range extensionGroupMap {
		if pg.PolicyType == policyType {
			delete(extensionGroupMap, id)
		}
	}
}

// UnregisterExtensionGroupsByExtension removes all extension groups for a given extension name.
func UnregisterExtensionGroupsByExtension(extName string) {
	extensionGroupMu.Lock()
	defer extensionGroupMu.Unlock()
	for id, pg := range extensionGroupMap {
		if pg.ExtensionName == extName {
			delete(extensionGroupMap, id)
		}
	}
}
