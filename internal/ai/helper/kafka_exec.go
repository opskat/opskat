package helper

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ExecKafkaOnAsset is the permission-check-free execution entry point used by the
// unified exec tool — the only path to Kafka now that the seven kafka_* tools and their
// in-handler permission checks are gone. handleExec checks permission before calling it.
// scope is meaningless for Kafka and is ignored — the target resource is named by the
// command's target position (see internal/ai/skills/kafka/SKILL.md).
//
// 分派到既有的 HandleKafka* 而不是直连 kafka_svc：那 7 个函数里的 operation 分派、
// kafka_args.go 的 ~20 个 *RequestFromArgs、结果序列化都已写好且有测试，重写一遍
// 就是 AGENTS.md 说的第二份实现。
func ExecKafkaOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	c, err := resolveKafkaCommand(command)
	if err != nil {
		return "", err
	}
	args, err := kafkaFlagsToArgs(c, asset.ID)
	if err != nil {
		return "", err
	}

	handle, ok := kafkaFamilyHandlers[c.Family]
	if !ok {
		return "", fmt.Errorf("unknown kafka resource family %q; supported: %s",
			c.Family, strings.Join(slices.Sorted(maps.Keys(kafkaFamilyHandlers)), ", "))
	}
	return handle(ctx, args)
}

// kafkaFamilyHandlers 把 DSL 的 family 映射到执行它的 handler。
//
// 写成表而不是 switch，是为了让这份 wiring 可以被直接断言。接错线
// （`case "topic"` 接到 HandleKafkaConsumerGroup）在运行期是静默的：删 topic 的请求
// 走进 consumer group 处理器，最好的结局是一个看不懂的错误，最坏的结局是同一资产上
// **另一类资源**被操作。
//
// 之前 TestExecKafkaOnAsset_DispatchesToItsOwnFamilyHandler 用"权限 checker 收到哪个
// 家族的策略串"当指纹，但权限检查已经从 HandleKafka* 内部上移到 handleExec，那个观察点
// 没有了；而靠错误文本区分不出 acl —— 它的三个 operation（list / create / delete）在
// topic / connect 等 handler 里都存在，接错线也只会得到同一个"Kafka 配置为空"。
// 所以把控制流换成数据，让测试直接比对函数指针，并断言 family 集合与 kafkaVerbs 一致。
var kafkaFamilyHandlers = map[string]func(context.Context, map[string]any) (string, error){
	"cluster":        HandleKafkaCluster,
	"topic":          HandleKafkaTopic,
	"consumer-group": HandleKafkaConsumerGroup,
	"acl":            HandleKafkaACL,
	"schema":         HandleKafkaSchema,
	"connect":        HandleKafkaConnect,
	"message":        HandleKafkaMessage,
}

// CanonicalizeKafkaCommand 把模型给的富命令串规范化为策略层的双 token 串
// "<action> <resource>"——与已删除的 7 个 kafka_* 工具逐字节相同的形式（用户库里
// 存着的 CmdPolicy 与 grant 都是照那个形式写的，不能变），理由见
// KafkaCommand.PolicyString 的注释。
//
// 排在权限检查之前（handleExec 的顺序，见 internal/ai/tool/tool_handlers_unified.go）：
// 语法错误、未知 flag、缺必填 flag 都必然导致执行失败，必须在弹审批对话框之前失败，
// 否则用户先被打断批准一次（选 "allow all" 还会落一条常驻 grant），命令才失败。
func CanonicalizeKafkaCommand(_ *asset_entity.Asset, command string) (string, error) {
	c, err := resolveKafkaCommand(command)
	if err != nil {
		return "", err
	}
	return c.PolicyString()
}

// resolveKafkaCommand 解析并做 per-verb 的 flag 校验。
//
// 规范化路径与执行路径必须都调它（mongo 侧的 resolveMongoCommand 同形）：统一 exec 把
// **原始**命令交给执行器、把**规范化后**的串交给权限检查，只在一边校验就等于没校验。
func resolveKafkaCommand(command string) (*KafkaCommand, error) {
	c, err := ParseKafkaCommand(command)
	if err != nil {
		return nil, err
	}
	if err := validateKafkaFlags(c); err != nil {
		return nil, err
	}
	return c, nil
}

// kafkaFlagSpec 是某个 (family, verb) 认得的 flag 集合，以及每个 flag 值的形状。
type kafkaFlagSpec struct {
	// required：缺了它这条命令必然跑不成，且这一点在**连上集群之前**就能判定——
	// 要么 kafka_args.go 的 *RequestFromArgs 当场报错，要么 kafka_svc 里有一条
	// **无条件**的校验（如 CreateTopic 的 `Partitions <= 0`）。
	//
	// 仍然不收进来的是 kafka_svc 里的**条件性**规则：acl 的 resource-name 取决于
	// resource-type 是不是 cluster、reset-offset 的 offset/timestamp 取决于 mode。
	// 把条件性规则抄过来才是第二处真相——service 放宽时这边会静默地多拒。
	required kafkaFlags
	optional kafkaFlags
}

// kafkaFlags 把 flag 名（DSL 的连字符写法）映射到值的形状校验；nil 表示自由文本。
type kafkaFlags = map[string]kafkaFlagCheck

// kafkaFlagCheck 校验单个 flag 值的形状。
//
// 每个非平凡的形状都**直接调用真正会消费这个值的那个构造器**，而不是另写一遍解析：
// 形状校验与实际解析必须不可能分叉，否则就会出现"校验放行、构造器报错"——正是这层
// 要消灭的"批准之后必然失败"。代价是构造器被调用两次（一次校验、一次真执行），
// 都是纯字符串解析，无副作用。
type kafkaFlagCheck func(value string) error

// kafkaText 表示不约束形状（自由文本）。写成具名的 nil，比在表里散落 nil 好读。
var kafkaText kafkaFlagCheck

// kafkaInt 要求十进制整数，判定谓词与 aictx.ArgInt64 的 string 分支完全一致——
// 这一条是本次要堵的静默缺陷本体：`--limit=1,000` / `1_000` / `1e3` / `3.0` today
// 都会被 ArgInt64 解析成 0，然后 service 套用默认值（browse 是 50 条），
// 用户批准"取 1000 条"、拿到 50 条，全程没有任何错误。
func kafkaInt(value string) error {
	if _, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err != nil {
		return fmt.Errorf("must be a decimal integer, got %q", value)
	}
	return nil
}

// kafkaBool 只收 true / false。ArgBool 把任何非 "true" 的值都读成 false，
// 所以 `--permanent=yes` 会被静默当成"不永久删除"。
func kafkaBool(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "false":
		return nil
	}
	return fmt.Errorf("must be true or false, got %q", value)
}

func kafkaPartition(value string) error {
	_, err := ArgOptionalPartition(map[string]any{"partition": value})
	return err
}

func kafkaStringMapJSON(value string) error {
	_, err := KafkaStringMapFromJSON(value)
	return err
}

func kafkaInt32SliceJSON(value string) error {
	_, err := KafkaInt32SliceFromJSON(value)
	return err
}

func kafkaSchemaRefsJSON(value string) error {
	_, err := KafkaSchemaReferencesFromJSON(value)
	return err
}

func kafkaProduceHeadersJSON(value string) error {
	_, err := KafkaProduceHeadersFromArgs(map[string]any{"headers": value})
	return err
}

// kafkaConfigUpdatesJSON 除了 JSON 形状还要求每项的 name 非空。
//
// 不是可有可无的加码：TopicConfigMutation 的 JSON 字段是 `name`，而 `[{"key":"retention.ms",
// "value":"1000"}]` 这种写法（Plan 的 SKILL.md 草稿就是这么写的）会被 encoding/json
// **静默**接受——未知字段直接丢弃，Name 落成空串。之后 topic_admin.go:184 报
// "配置名称不能为空"，无条件、且发生在审批之后。空 name 在这里没有任何合法用途。
func kafkaConfigUpdatesJSON(value string) error {
	req, err := KafkaAlterTopicConfigRequestFromArgs(0, map[string]any{"config_updates": value})
	if err != nil {
		return err
	}
	for i, cfg := range req.Configs {
		if strings.TrimSpace(cfg.Name) == "" {
			return fmt.Errorf(`entry %d has an empty "name"; each config update must be {"name":"<config>","value":"<value>"}`, i)
		}
	}
	return nil
}

func kafkaDeleteRecordsJSON(value string) error {
	_, err := KafkaDeleteRecordsRequestFromArgs(0, map[string]any{"records": value})
	return err
}

// kafkaConnectorConfigJSON 除了 JSON 形状还挡住 `--config='{}'`：
// KafkaConnectorConfigRequestFromArgs 对空 map 也报 "config is required"，
// 而 "{}" 是非空字符串、过得了必填检查。
func kafkaConnectorConfigJSON(value string) error {
	_, err := KafkaConnectorConfigRequestFromArgs(0, map[string]any{"config": value})
	return err
}

// kafkaFlagRules 列出每个 (family, verb) 认得的 flag 与其值的形状，flag 名用 DSL 的
// 连字符写法（与 SKILL.md 一致），比较时两边都过 normalizeKafkaFlagName，所以下划线
// 写法同样收。
//
// 每一条都对着 kafka_helper.go 各 Handle* 与 kafka_args.go 各 *RequestFromArgs
// 实际读的 key 核过。不在表里的 flag 一律拒绝：今天的 map[string]any 参数对多余 key
// 是静默丢弃，于是 `topic create orders --paritions=3`（拼错）会按"分区数 0"往下走，
// 而审批弹窗上明明写着 --paritions=3 —— 批准的是一件事，执行的是另一件。
//
// 这张表是"哪些 flag 有用"的第二处真相（第一处是各 *RequestFromArgs 读的 key），
// 两个方向的漂移都会静默：多写一个没人读的 flag = 用户以为设置生效了其实没有；
// 少写一个 = 那个参数在统一 exec 下不可达。TestKafkaFlagRules_EveryFlagReachesItsRequestField
// 遍历整张表、断言每个 flag 都真的落到对应 request 字段上，把两个方向一起钉死。
var kafkaFlagRules = map[string]map[string]kafkaFlagSpec{
	"cluster": {
		"overview":        {},
		"brokers":         {},
		"broker-config":   {optional: kafkaFlags{"broker-id": kafkaInt}},
		"cluster-configs": {},
	},
	"topic": {
		"list": {optional: kafkaFlags{
			"include-internal": kafkaBool, "search": kafkaText, "page": kafkaInt, "page-size": kafkaInt,
		}},
		"describe": {},
		// partitions / replication-factor 必填：topic_admin.go:21,24 无条件要求两者 > 0，
		// 而那两条检查在 withClient 内部——不在这里拦，用户会先被弹审批、再白连一次集群。
		"create": {
			required: kafkaFlags{"partitions": kafkaInt, "replication-factor": kafkaInt},
			optional: kafkaFlags{"configs": kafkaStringMapJSON},
		},
		"delete":              {},
		"update-config":       {required: kafkaFlags{"config-updates": kafkaConfigUpdatesJSON}},
		"increase-partitions": {required: kafkaFlags{"partition-count": kafkaInt}}, // topic_admin.go:99 无条件 > 0
		"delete-records":      {required: kafkaFlags{"records": kafkaDeleteRecordsJSON}},
	},
	"consumer-group": {
		"list":     {},
		"describe": {},
		"reset-offset": {
			// topic 必填：consumer_group_admin.go:84 无条件拒空。
			// offset / timestamp-millis 有意留作可选——它们只在对应 mode 下才必填，
			// 属于上面说的条件性规则。
			required: kafkaFlags{"topic": kafkaText},
			optional: kafkaFlags{
				"partitions": kafkaInt32SliceJSON, "mode": kafkaText,
				"offset": kafkaInt, "timestamp-millis": kafkaInt,
			},
		},
		"delete": {},
	},
	"acl": {
		"list": {optional: kafkaFlags{
			"resource-type": kafkaText, "resource-name": kafkaText, "pattern-type": kafkaText,
			"principal": kafkaText, "host": kafkaText, "acl-operation": kafkaText,
			"permission": kafkaText, "page": kafkaInt, "page-size": kafkaInt,
		}},
		// acl_admin.go:141-206：resource-type / acl-operation / permission / principal
		// 四个无条件拒空，delete 还额外要求 host。resource-name 留作可选——它只在
		// resource-type 不是 cluster 时才必填，是条件性规则。
		"create": {
			required: kafkaFlags{
				"resource-type": kafkaText, "principal": kafkaText,
				"acl-operation": kafkaText, "permission": kafkaText,
			},
			optional: kafkaFlags{"resource-name": kafkaText, "pattern-type": kafkaText, "host": kafkaText},
		},
		"delete": {
			required: kafkaFlags{
				"resource-type": kafkaText, "principal": kafkaText,
				"acl-operation": kafkaText, "permission": kafkaText, "host": kafkaText,
			},
			optional: kafkaFlags{"resource-name": kafkaText, "pattern-type": kafkaText},
		},
	},
	"schema": {
		"list-subjects": {},
		"list-versions": {},
		"describe":      {optional: kafkaFlags{"version": kafkaText}},
		// schema 必填：schema_registry.go:101,128 无条件拒空。
		"check-compatibility": {
			required: kafkaFlags{"schema": kafkaText},
			optional: kafkaFlags{"version": kafkaText, "schema-type": kafkaText, "references": kafkaSchemaRefsJSON},
		},
		"register": {
			required: kafkaFlags{"schema": kafkaText},
			optional: kafkaFlags{"schema-type": kafkaText, "references": kafkaSchemaRefsJSON},
		},
		"delete": {optional: kafkaFlags{"version": kafkaText, "permanent": kafkaBool}},
	},
	"connect": {
		"list-clusters":   {},
		"list-connectors": {optional: kafkaFlags{"cluster": kafkaText}},
		"describe":        {optional: kafkaFlags{"cluster": kafkaText}},
		"create": {
			required: kafkaFlags{"config": kafkaConnectorConfigJSON},
			optional: kafkaFlags{"cluster": kafkaText},
		},
		"update-config": {
			required: kafkaFlags{"config": kafkaConnectorConfigJSON},
			optional: kafkaFlags{"cluster": kafkaText},
		},
		"pause":  {optional: kafkaFlags{"cluster": kafkaText}},
		"resume": {optional: kafkaFlags{"cluster": kafkaText}},
		"restart": {optional: kafkaFlags{
			"cluster": kafkaText, "include-tasks": kafkaBool, "only-failed": kafkaBool,
		}},
		"delete": {optional: kafkaFlags{"cluster": kafkaText}},
	},
	"message": {
		"browse": {optional: kafkaFlags{
			"partition": kafkaPartition, "start-mode": kafkaText, "offset": kafkaInt,
			"timestamp-millis": kafkaInt, "limit": kafkaInt, "max-bytes": kafkaInt,
			"decode-mode": kafkaText, "max-wait-millis": kafkaInt,
		}},
		"inspect": {
			required: kafkaFlags{"partition": kafkaPartition, "offset": kafkaInt},
			optional: kafkaFlags{"max-bytes": kafkaInt, "decode-mode": kafkaText, "max-wait-millis": kafkaInt},
		},
		"produce": {optional: kafkaFlags{
			"partition": kafkaPartition, "key": kafkaText, "key-encoding": kafkaText,
			"value": kafkaText, "value-encoding": kafkaText,
			"headers": kafkaProduceHeadersJSON, "timestamp-millis": kafkaInt,
		}},
	},
}

// kafkaProvidedFlag 记住模型实际写下的拼写：报重复时要把两种写法都还给它，
// 只回归一化后的名字会让它看不出自己写了哪两个。
type kafkaProvidedFlag struct {
	name  string
	value string
}

// validateKafkaFlags 拒绝这一层——也只有这一层——能判定的坏 flag。
// per-verb 的合法 flag 集合是执行器独有的知识：cmdline 只做词法，kafka_dsl.go 只认
// family/verb/target。
func validateKafkaFlags(c *KafkaCommand) error {
	spec, ok := kafkaFlagRules[c.Family][c.Verb]
	if !ok {
		return fmt.Errorf("no flag rules registered for %q %q", c.Family, c.Verb)
	}

	known := make(map[string]kafkaFlagCheck, len(spec.required)+len(spec.optional))
	for name, check := range spec.required {
		known[normalizeKafkaFlagName(name)] = check
	}
	for name, check := range spec.optional {
		known[normalizeKafkaFlagName(name)] = check
	}

	// unknown 排序后一次性报出：c.Flags 是 map，只报"碰到的第一个"会让同一条命令
	// 在不同次调用里报出不同的 flag 名，而这段文本是发给模型的（同 ParseMongoCommand）。
	var unknown []string
	provided := make(map[string]kafkaProvidedFlag, len(c.Flags))
	for name, value := range c.Flags {
		normalized := normalizeKafkaFlagName(name)
		if _, ok := known[normalized]; !ok {
			unknown = append(unknown, name)
			continue
		}
		// 连字符与下划线两种写法归一后是同一个 args key，后写的会盖掉先写的，
		// cmdline 的重名检查看的是原始拼写、拦不住这一对。报错时把两个拼写排序后
		// 输出，否则同一条命令每次报出的顺序都不一样（map 迭代顺序随机）。
		if prev, dup := provided[normalized]; dup {
			names := []string{prev.name, name}
			slices.Sort(names)
			return fmt.Errorf("flags --%s and --%s are the same parameter %q; pass it once",
				names[0], names[1], normalized)
		}
		provided[normalized] = kafkaProvidedFlag{name: name, value: value}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("unknown flag(s) --%s for %q %q; supported: %s",
			strings.Join(unknown, ", --"), c.Family, c.Verb, kafkaSupportedFlagList(spec))
	}

	// 必填先于形状：`--config` 完全没给和给了个坏值是两条不同的提示，先报缺失更准。
	var missing []string
	for name := range spec.required {
		if strings.TrimSpace(provided[normalizeKafkaFlagName(name)].value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("%q %q requires --%s", c.Family, c.Verb, strings.Join(missing, ", --"))
	}

	// 形状检查排在最后，但仍在权限检查之前——这才是重点：一个 --limit=1,000 或
	// --records=notjson 今天能一路走到审批弹窗，批准之后才在构造器里失败（或者更糟，
	// 被静默当成 0 / 默认值）。名字排序遍历，保证同一条命令每次报出同一个 flag。
	for _, normalized := range slices.Sorted(maps.Keys(provided)) {
		check := known[normalized]
		if check == nil {
			continue
		}
		flag := provided[normalized]
		if err := check(flag.value); err != nil {
			return fmt.Errorf("invalid value for --%s: %w", flag.name, err)
		}
	}
	return nil
}

func kafkaSupportedFlagList(spec kafkaFlagSpec) string {
	all := slices.Concat(slices.Collect(maps.Keys(spec.required)), slices.Collect(maps.Keys(spec.optional)))
	if len(all) == 0 {
		return "this verb takes no flags"
	}
	slices.Sort(all)
	return "--" + strings.Join(all, ", --")
}

// kafkaFlagsToArgs 把 KafkaCommand 摊平成既有 Handle* 认得的 map[string]any。
//
// 数值一律以 string 进 map，由 aictx.ArgInt / ArgInt64 解析（它们的 string 分支是
// 本次补上的）：在这里提前 strconv 转型是在调用点兜住一个坏的生产者，下一个调用方
// 还要再踩一次。
func kafkaFlagsToArgs(c *KafkaCommand, assetID int64) (map[string]any, error) {
	args := make(map[string]any, len(c.Flags)+3)
	args["asset_id"] = assetID
	args["operation"] = c.operation()
	for name, value := range c.Flags {
		args[normalizeKafkaFlagName(name)] = value
	}
	if c.Target != "" {
		key, ok := kafkaTargetArgNames[c.Family]
		if !ok {
			return nil, fmt.Errorf("kafka resource family %q has no target argument, got target %q; supported: %s",
				c.Family, c.Target, strings.Join(slices.Sorted(maps.Keys(kafkaTargetArgNames)), ", "))
		}
		args[key] = c.Target
	}
	return args, nil
}

// kafkaTargetArgNames 把 family 映射到"资源名"在 args 里的 key，逐条对着
// kafka_helper.go 里对应 Handle* 实际读的 key 核过：
//
//	topic / message → HandleKafkaTopic / HandleKafkaMessage 读 "topic"
//	consumer-group  → HandleKafkaConsumerGroup 读 "group"
//	schema          → HandleKafkaSchema 读 "subject"
//	connect         → HandleKafkaConnect 读 "connector"
//
// 写错一个 key 不会报错，只会让 target 静默丢失，落成"列出了全部 topic"或"操作了
// 默认资源"——而审批弹窗上显示的仍然是带 target 的那条命令。
//
// cluster / acl 不在表里：它们在 kafkaVerbs 里没有任何 needsTarget 的 verb，
// 策略串的 resource 位恒为 "*"。新增一个带 target 的 family 却忘了登记，会被
// TestKafkaFlagsToArgs_TargetMatchesPolicyResource 挡下，运行期则 fail closed。
var kafkaTargetArgNames = map[string]string{
	"topic":          "topic",
	"message":        "topic",
	"consumer-group": "group",
	"schema":         "subject",
	"connect":        "connector",
}

// normalizeKafkaFlagName 把 DSL 的连字符 flag 名换成 args 的下划线 key
// （--replication-factor → replication_factor）。
func normalizeKafkaFlagName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}
