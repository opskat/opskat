package helper

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// KafkaCommand 是 kafka 富命令串的结构形式。
//
// 格式：`<family> <verb> [target] [--flags]`
//
// family 对应今天的 7 个 kafka_* 工具，verb 对应各工具的 operation 参数，
// target 是资源名（topic / group / subject / connector），flags 承载其余全部参数。
// 之所以让 family 占 Verb 位、verb 退到第一个 positional：cmdline 只认"第一个词是
// 动词"这一条词法规则，kafka 的资源维度比其他类型多一层，把 family 放在最前面
// 才能让 `topic delete orders` 读起来仍然是命令行的样子。
type KafkaCommand struct {
	Family string
	Verb   string
	Target string
	Flags  map[string]string
}

// kafkaVerbs 列出每个 family 支持的 verb，以及该 verb 是否需要 target。
//
// verb 名与今天工具的 operation 值一一对应，但改用连字符（reset-offset 而非
// reset_offset），与命令行惯例一致；映射在 kafkaOperationFor 里做。
// 同义 operation（get / describe、brokers / list_brokers、get_connector / get）
// 各只保留一个写法：它们在 kafka_command.go 里落到同一个 case、产出同一个策略串，
// 多留一个只是让模型多一种猜法。
var kafkaVerbs = map[string]map[string]bool{
	"cluster": {
		"overview": false, "brokers": false,
		"broker-config": false, "cluster-configs": false,
	},
	"topic": {
		"list": false, "describe": true, "create": true, "delete": true,
		"update-config": true, "increase-partitions": true, "delete-records": true,
	},
	"consumer-group": {
		"list": false, "describe": true, "reset-offset": true, "delete": true,
	},
	"acl": {
		"list": false, "create": false, "delete": false,
	},
	"schema": {
		"list-subjects": false, "list-versions": true, "describe": true,
		"check-compatibility": true, "register": true, "delete": true,
	},
	"connect": {
		"list-clusters": false, "list-connectors": false, "describe": true,
		"create": true, "update-config": true,
		"pause": true, "resume": true, "restart": true, "delete": true,
	},
	"message": {
		"browse": true, "inspect": true, "produce": true,
	},
}

// kafkaOperationFor 把 DSL 的 verb 还原为现有 Kafka*Command 认得的 operation 值。
// 只有写法不同的才列在这里，其余（list / create / delete / describe / pause …）
// 由 operation 原样透传。
var kafkaOperationFor = map[string]string{
	"broker-config":       "get_broker_config",
	"cluster-configs":     "list_cluster_configs",
	"update-config":       "update_config",
	"increase-partitions": "increase_partitions",
	"delete-records":      "delete_records",
	"reset-offset":        "reset_offset",
	"list-subjects":       "list_subjects",
	"list-versions":       "list_versions",
	"check-compatibility": "check_compatibility",
	"list-clusters":       "list_clusters",
	"list-connectors":     "list_connectors",
}

// ParseKafkaCommand 解析富命令串。
func ParseKafkaCommand(s string) (*KafkaCommand, error) {
	parsed, err := cmdline.Parse(s)
	if err != nil {
		return nil, err
	}

	verbs, ok := kafkaVerbs[parsed.Verb]
	if !ok {
		return nil, fmt.Errorf("unknown kafka resource family %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(kafkaVerbs)), ", "))
	}
	if len(parsed.Args) == 0 {
		return nil, fmt.Errorf("missing verb for %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
	}

	verb := parsed.Args[0]
	needsTarget, ok := verbs[verb]
	if !ok {
		return nil, fmt.Errorf("unknown verb %q for %q; supported: %s",
			verb, parsed.Verb, strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
	}
	if len(parsed.Args) > 2 {
		return nil, fmt.Errorf("kafka commands take at most two positional arguments (<family> <verb> [target]); got %d", len(parsed.Args)+1)
	}

	c := &KafkaCommand{Family: parsed.Verb, Verb: verb, Flags: parsed.Flags}
	if len(parsed.Args) == 2 {
		c.Target = parsed.Args[1]
	}
	if needsTarget && c.Target == "" {
		return nil, fmt.Errorf("verb %q requires a target: %s %s <name>", verb, c.Family, verb)
	}
	return c, nil
}

// Render 是 ParseKafkaCommand 的逆函数（TestKafkaCommand_RoundTrip 锁住）。
//
// 拒绝以 "--" 开头的 Target，理由与 MongoCommand.Render 拒绝同形 Collection 完全一致
// （见该函数注释）：cmdline.Parse 判断 positional 还是 flag，看的是**剥掉引号之后**的
// 词是否以 "--" 开头（internal/ai/cmdline/cmdline.go:162），判断发生在引号信息已经
// 丢失之后，所以加引号救不了。ParseKafkaCommand 自己永远产不出这种 Target，但
// KafkaCommand 是导出结构体，统一 exec 会用结构化的工具参数直接构造它、绕开 Parse——
// 拒绝好过悄悄产出一个 Render 完语义就变了的串。
func (c *KafkaCommand) Render() (string, error) {
	if strings.HasPrefix(c.Target, "--") {
		return "", fmt.Errorf("target %q cannot start with \"--\": it would be misread as a flag when the rendered command is re-parsed", c.Target)
	}

	cmd := &cmdline.Command{Verb: c.Family, Args: []string{c.Verb}, Flags: c.Flags}
	if c.Target != "" {
		cmd.Args = append(cmd.Args, c.Target)
	}
	return cmd.Render(), nil
}

// PolicyString 返回策略匹配用的 "<action> <resource>" 串。
//
// **必须**恰好两个 token，且与今天 7 个 kafka_* 工具的输出逐字节相同——所以这里
// 直接复用现有的 Kafka*Command 函数（internal/ai/helper/kafka_command.go），不重写一遍。
// 理由见 TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm 的注释：
// splitKafkaRule 要求恰好 2 段，不满足时 MatchKafkaRule 静默返回 false，
// 于是 BuiltinKafkaDangerousDeny 的 deny 规则不再匹配任何命令（fail-open）。
func (c *KafkaCommand) PolicyString() (string, error) {
	op := c.operation()
	switch c.Family {
	case "cluster":
		return KafkaClusterCommand(op)
	case "topic":
		return KafkaTopicCommand(op, c.Target)
	case "consumer-group":
		return KafkaConsumerGroupCommand(op, c.Target)
	case "acl":
		return KafkaACLCommand(op)
	case "schema":
		return KafkaSchemaCommand(op, c.Target)
	case "connect":
		return KafkaConnectCommand(op, c.Target)
	case "message":
		return KafkaMessageCommand(op, c.Target)
	default:
		return "", fmt.Errorf("unknown kafka resource family %q", c.Family)
	}
}

// operation 返回现有 Kafka*Command 认得的 operation 值。
func (c *KafkaCommand) operation() string {
	if mapped, ok := kafkaOperationFor[c.Verb]; ok {
		return mapped
	}
	return c.Verb
}
