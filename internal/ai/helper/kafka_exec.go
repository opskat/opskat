package helper

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ExecKafkaOnAsset is the permission-check-free execution entry point used by the
// unified exec tool.
// The seven kafka_* tools are removed alongside their in-handler permission checks in a
// later task; until then the two paths coexist and this one is not yet registered.
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

	switch c.Family {
	case "cluster":
		return HandleKafkaCluster(ctx, args)
	case "topic":
		return HandleKafkaTopic(ctx, args)
	case "consumer-group":
		return HandleKafkaConsumerGroup(ctx, args)
	case "acl":
		return HandleKafkaACL(ctx, args)
	case "schema":
		return HandleKafkaSchema(ctx, args)
	case "connect":
		return HandleKafkaConnect(ctx, args)
	case "message":
		return HandleKafkaMessage(ctx, args)
	default:
		return "", fmt.Errorf("unknown kafka resource family %q", c.Family)
	}
}

// CanonicalizeKafkaCommand 把模型给的富命令串规范化为策略层的双 token 串
// "<action> <resource>"——与今天 7 个 kafka_* 工具逐字节相同的形式，理由见
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

// kafkaFlagSpec 是某个 (family, verb) 认得的 flag 集合。
type kafkaFlagSpec struct {
	// required 的判定标准只有一条：缺了它，kafka_args.go 里对应的 *RequestFromArgs
	// **当场**就会报错，也就是这条命令在连上集群之前就已经确定跑不成。
	// 不把 kafka_svc 里的校验（如 CreateTopic 要求分区数 > 0）抄进来：那些规则有的
	// 是条件性的（acl 的 resource-name 取决于 resource-type、reset-offset 的
	// offset/timestamp 取决于 mode），抄一份就是第二处真相，service 放宽时这里会
	// 静默地多拒。
	required []string
	optional []string
}

// kafkaFlagRules 列出每个 (family, verb) 认得的 flag，用 DSL 的连字符写法
// （与 SKILL.md 一致），比较时两边都过 normalizeKafkaFlagName，所以下划线写法同样收。
//
// 每一条都对着 kafka_helper.go 各 Handle* 与 kafka_args.go 各 *RequestFromArgs
// 实际读的 key 核过。不在表里的 flag 一律拒绝：今天的 map[string]any 参数对多余 key
// 是静默丢弃，于是 `topic create orders --paritions=3`（拼错）会按"分区数 0"往下走，
// 而审批弹窗上明明写着 --paritions=3 —— 批准的是一件事，执行的是另一件。
var kafkaFlagRules = map[string]map[string]kafkaFlagSpec{
	"cluster": {
		"overview":        {},
		"brokers":         {},
		"broker-config":   {optional: []string{"broker-id"}},
		"cluster-configs": {},
	},
	"topic": {
		"list":                {optional: []string{"include-internal", "search", "page", "page-size"}},
		"describe":            {},
		"create":              {optional: []string{"partitions", "replication-factor", "configs"}},
		"delete":              {},
		"update-config":       {required: []string{"config-updates"}},
		"increase-partitions": {optional: []string{"partition-count"}},
		"delete-records":      {required: []string{"records"}},
	},
	"consumer-group": {
		"list":         {},
		"describe":     {},
		"reset-offset": {optional: []string{"topic", "partitions", "mode", "offset", "timestamp-millis"}},
		"delete":       {},
	},
	"acl": {
		"list":   {optional: []string{"resource-type", "resource-name", "pattern-type", "principal", "host", "acl-operation", "permission", "page", "page-size"}},
		"create": {optional: []string{"resource-type", "resource-name", "pattern-type", "principal", "host", "acl-operation", "permission"}},
		"delete": {optional: []string{"resource-type", "resource-name", "pattern-type", "principal", "host", "acl-operation", "permission"}},
	},
	"schema": {
		"list-subjects":       {},
		"list-versions":       {},
		"describe":            {optional: []string{"version"}},
		"check-compatibility": {optional: []string{"version", "schema", "schema-type", "references"}},
		"register":            {optional: []string{"schema", "schema-type", "references"}},
		"delete":              {optional: []string{"version", "permanent"}},
	},
	"connect": {
		"list-clusters":   {},
		"list-connectors": {optional: []string{"cluster"}},
		"describe":        {optional: []string{"cluster"}},
		"create":          {required: []string{"config"}, optional: []string{"cluster"}},
		"update-config":   {required: []string{"config"}, optional: []string{"cluster"}},
		"pause":           {optional: []string{"cluster"}},
		"resume":          {optional: []string{"cluster"}},
		"restart":         {optional: []string{"cluster", "include-tasks", "only-failed"}},
		"delete":          {optional: []string{"cluster"}},
	},
	"message": {
		"browse":  {optional: []string{"partition", "start-mode", "offset", "timestamp-millis", "limit", "max-bytes", "decode-mode", "max-wait-millis"}},
		"inspect": {required: []string{"partition", "offset"}, optional: []string{"max-bytes", "decode-mode", "max-wait-millis"}},
		"produce": {optional: []string{"partition", "key", "key-encoding", "value", "value-encoding", "headers", "timestamp-millis"}},
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

	known := make(map[string]bool, len(spec.required)+len(spec.optional))
	for _, name := range slices.Concat(spec.required, spec.optional) {
		known[normalizeKafkaFlagName(name)] = true
	}

	// unknown 排序后一次性报出：c.Flags 是 map，只报"碰到的第一个"会让同一条命令
	// 在不同次调用里报出不同的 flag 名，而这段文本是发给模型的（同 ParseMongoCommand）。
	var unknown []string
	provided := make(map[string]kafkaProvidedFlag, len(c.Flags))
	for name, value := range c.Flags {
		normalized := normalizeKafkaFlagName(name)
		if !known[normalized] {
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

	var missing []string
	for _, name := range spec.required {
		if strings.TrimSpace(provided[normalizeKafkaFlagName(name)].value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("%q %q requires --%s", c.Family, c.Verb, strings.Join(missing, ", --"))
	}
	return nil
}

func kafkaSupportedFlagList(spec kafkaFlagSpec) string {
	all := slices.Concat(spec.required, spec.optional)
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
