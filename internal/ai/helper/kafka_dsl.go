package helper

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// KafkaCommand 是 kafka 富命令串的结构形式。
//
// 格式：`<family> <verb> [target] [--flags]`
//
// family 对应已删除的 7 个 kafka_* 工具，verb 对应各工具的 operation 参数，
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
// verb 名与旧工具的 operation 值一一对应，但改用连字符（reset-offset 而非
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

// kafkaCommandForm 把 (family, operation) 拼成模型真的能写出来的 exec 命令形式
// `<family> <verb>`。
//
// 面向模型的报错串专用：7 个 kafka_* 工具已删除，报错里再出现 kafka_topic 之类的
// 名字，等于让模型去调一个不存在的工具。
//
// 只做拼接，不把 operation 反查回 verb。反查曾经存在（由 kafkaOperationFor 反转得到），
// 但它一次也命中不了：调用点全在各 Kafka*Command / HandleKafka* 的 default 分支上，
// 而唯一的生产调用方 PolicyString 按 c.Family 分派、传的是 c.operation()，
// 两者都已被 ParseKafkaCommand 按 kafkaVerbs 校验过——属于别的 family 的 operation
// 根本到不了这里。反查唯一能改变的输出（把 reset_offset 显示成 reset-offset）
// 因此是一个到不了的分支，删掉而不是留着当承重噪音。
func kafkaCommandForm(family, operation string) string {
	return family + " " + operation
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
		// needsTarget 既是下界也是上界。只当下界用的话，`topic list orders`
		// （模型想说 describe orders）会解析通过、审批弹窗照原样显示这一串，
		// 而执行器把多出来的 orders 丢掉、列出集群里全部 topic —— 批准一件事、
		// 拿到另一件。ParseMongoCommand 在 collection 上有同形的守卫。
		if !needsTarget {
			return nil, fmt.Errorf("verb %q takes no target, got %q; use: %s %s", verb, parsed.Args[1], c.Family, verb)
		}
		c.Target = parsed.Args[1]
	}
	if needsTarget && c.Target == "" {
		return nil, fmt.Errorf("verb %q requires a target: %s %s <name>", verb, c.Family, verb)
	}
	if err := validateKafkaTarget(c.Target); err != nil {
		return nil, err
	}
	return c, nil
}

// validateKafkaTarget 拒绝含任何 Unicode 空白的 target。
//
// 策略串是 "<action> <resource>" 恰好两个 whitespace 分隔的 token，splitKafkaRule
// （internal/ai/policy/kafka_policy.go:50）用 strings.Fields 切、`len != 2` 就返回
// ok=false，MatchKafkaRule 随之对**任何**规则返回 false，既不报错也不记日志：
// 实测 `consumer-group delete 'my group'` 在 BuiltinKafkaDangerousDeny 下从 Deny
// 掉成 NeedConfirm —— deny 列表整个失效。
//
// 判定谓词特意用 unicode.IsSpace，也就是 strings.Fields 自己的切分谓词：这里要挡的
// 恰好是"会被 Fields 切开的字符"，用同一个谓词，检查和它保护的约束才不会各自漂移。
// 这也顺带覆盖了 shell 切词与 Fields 的错位——NBSP(U+00A0) / U+3000 不是 shell 分隔符，
// 能不带引号一路走进 Target，Fields 却认它们。
//
// 代价是明确的、也是有意接受的：名字里带空白的 consumer group / schema subject /
// connector 在统一 exec 下不可达。这严格优于现状——现状是够得着，但 deny 列表在那一次
// 调用里静默失效。别想着靠加引号绕过：引号会破坏与旧 7 个 kafka_* 工具的字节相同，
// 而 splitKafkaRule 也不认引号。
func validateKafkaTarget(target string) error {
	for i, r := range target {
		if unicode.IsSpace(r) {
			return fmt.Errorf("target %q cannot contain whitespace (found %q at byte %d): kafka permission rules are matched as exactly two space-separated tokens \"<action> <resource>\", so a name containing whitespace cannot be authorized at all", target, r, i)
		}
	}
	return nil
}

// Render 是 ParseKafkaCommand 的逆函数（TestKafkaCommand_RoundTrip 锁住）。
//
// 拒绝以 "--" 开头的 Target，理由与 MongoCommand.Render 拒绝同形 Collection 完全一致
// （见该函数注释）：cmdline.Parse 判断 positional 还是 flag，看的是**剥掉引号之后**的
// 词是否以 "--" 开头（internal/ai/cmdline/cmdline.go:162），判断发生在引号信息已经
// 丢失之后，所以加引号救不了。ParseKafkaCommand 自己永远产不出这种 Target，但
// KafkaCommand 是导出结构体，统一 exec 会用结构化的工具参数直接构造它、绕开 Parse——
// 拒绝好过悄悄产出一个 Render 完语义就变了的串。
// 同理还要校验 flag 名：cmdline.Command.Render 明说手写字面量不受 Parse 的入口校验
// 保护、由调用方保证 flag 名合法，KafkaCommand.Render 就是那个调用方。mongo 靠写死
// db / query 两个 flag 名绕开了这件事，kafka 接受任意 flag 名，绕不开——
// `{"cfg=x": "1"}` 渲染成 `--cfg=x=1`，重新解析后是 flag "cfg" 值 "x=1"。
// 判定复用 cmdline.ValidFlagName，不在这里重抄一份 safeUnquotedWord。
func (c *KafkaCommand) Render() (string, error) {
	if strings.HasPrefix(c.Target, "--") {
		return "", fmt.Errorf("target %q cannot start with \"--\": it would be misread as a flag when the rendered command is re-parsed", c.Target)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		if !cmdline.ValidFlagName(name) {
			return "", fmt.Errorf("invalid flag name %q: it would not survive re-parsing of the rendered command; use only letters, digits, and _@%%+:,./- characters", name)
		}
	}

	cmd := &cmdline.Command{Verb: c.Family, Args: []string{c.Verb}, Flags: c.Flags}
	if c.Target != "" {
		cmd.Args = append(cmd.Args, c.Target)
	}
	return cmd.Render(), nil
}

// PolicyString 返回策略匹配用的 "<action> <resource>" 串。
//
// **必须**恰好两个 token，且与已删除的 7 个 kafka_* 工具的输出逐字节相同——所以这里
// 直接复用现有的 Kafka*Command 函数（internal/ai/helper/kafka_command.go），不重写一遍。
// 理由见 TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm 的注释：
// splitKafkaRule 要求恰好 2 段，不满足时 MatchKafkaRule 静默返回 false，
// 于是 BuiltinKafkaDangerousDeny 的 deny 规则不再匹配任何命令（fail-open）。
//
// 后置条件（结果必须恰好 2 个 Fields）不是可有可无的防御性代码，别顺手删：
// ParseKafkaCommand 的 validateKafkaTarget 只守住"解析命令串"这一条路，而 KafkaCommand
// 是导出结构体，统一 exec 会从结构化的工具参数直接构造它（mongo 侧 opsctl batch 的
// mongo 类型条目就是这么做的，见 cmd/opsctl/command/batch.go 的 executeBatchItem）、
// 完全绕开解析。这条后置条件是那条缝上唯一的守卫，而这条缝一旦破了
// 是静默的——不满足 2-token 时 splitKafkaRule 返回 ok=false，deny 规则一条都不匹配。
// 宁可在这里 fail closed 报错，也不能把一个匹配不上任何规则的串交出去。
func (c *KafkaCommand) PolicyString() (string, error) {
	s, err := c.policyString()
	if err != nil {
		return "", err
	}
	if fields := len(strings.Fields(s)); fields != 2 {
		return "", fmt.Errorf("kafka permission command %q has %d whitespace-separated tokens, want exactly 2: it would match no policy rule at all (neither allow nor deny)", s, fields)
	}
	return s, nil
}

func (c *KafkaCommand) policyString() (string, error) {
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
