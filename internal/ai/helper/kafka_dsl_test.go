package helper

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseKafkaCommand(t *testing.T) {
	cases := []struct {
		in   string
		want KafkaCommand
	}{
		{`topic list`, KafkaCommand{Family: "topic", Verb: "list", Flags: map[string]string{}}},
		{`topic describe orders`, KafkaCommand{Family: "topic", Verb: "describe", Target: "orders", Flags: map[string]string{}}},
		{`topic create orders --partitions=3 --replication-factor=2`,
			KafkaCommand{Family: "topic", Verb: "create", Target: "orders",
				Flags: map[string]string{"partitions": "3", "replication-factor": "2"}}},
		{`message produce orders --key=k1 --value='{"a": 1}'`,
			KafkaCommand{Family: "message", Verb: "produce", Target: "orders",
				Flags: map[string]string{"key": "k1", "value": `{"a": 1}`}}},
		{`consumer-group reset-offset mygroup --topic=orders --mode=earliest`,
			KafkaCommand{Family: "consumer-group", Verb: "reset-offset", Target: "mygroup",
				Flags: map[string]string{"topic": "orders", "mode": "earliest"}}},
	}
	for _, c := range cases {
		got, err := ParseKafkaCommand(c.in)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(*got, c.want) {
			t.Fatalf("ParseKafkaCommand(%q) = %#v, want %#v", c.in, *got, c.want)
		}
	}
}

func TestParseKafkaCommand_RejectsUnknownFamilyOrVerb(t *testing.T) {
	cases := []string{
		`nonsense list`,
		`topic nonsense orders`,
		`topic`,
	}
	for _, in := range cases {
		if _, err := ParseKafkaCommand(in); err == nil {
			t.Fatalf("ParseKafkaCommand(%q) = nil error, want rejection", in)
		}
	}
}

func TestKafkaCommand_RoundTrip(t *testing.T) {
	cmds := []KafkaCommand{
		{Family: "topic", Verb: "list", Flags: map[string]string{}},
		{Family: "topic", Verb: "create", Target: "orders", Flags: map[string]string{"partitions": "3"}},
		{Family: "message", Verb: "produce", Target: "t", Flags: map[string]string{"value": `{"a": "x y"}`}},
		{Family: "message", Verb: "browse", Target: "t", Flags: map[string]string{"limit": "100", "partition": "0"}},
		{Family: "acl", Verb: "create", Flags: map[string]string{"principal": "User:alice", "resource-type": "topic"}},
		{Family: "schema", Verb: "register", Target: "subj", Flags: map[string]string{"schema": `{"type":"record"}`}},
		{Family: "connect", Verb: "restart", Target: "conn", Flags: map[string]string{}},
	}
	for _, want := range cmds {
		rendered, err := want.Render()
		if err != nil {
			t.Fatalf("Render(%#v) unexpected error: %v", want, err)
		}
		got, err := ParseKafkaCommand(rendered)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(Render(%#v)) = error %v (rendered: %s)", want, err, rendered)
		}
		if !reflect.DeepEqual(*got, want) {
			t.Fatalf("round-trip = %#v, want %#v (rendered: %s)", *got, want, rendered)
		}
	}
}

// TestKafkaCommand_RenderRejectsFlagLikeTarget 锁住 Render 对 "--" 开头 Target 的拒绝。
// Target 是导出字段，统一 exec 会用结构化的 topic / group 工具参数直接构造
// KafkaCommand 而绕开 ParseKafkaCommand（ParseKafkaCommand 自己永远产不出这种值——
// 这种词在切词阶段就被当 flag 吃掉了）。cmdline.Parse 只看剥引号之后的词是否以
// "--" 开头来判定 positional 还是 flag，所以加引号救不了：`topic describe --foo`
// 重新解析后 Target 变空、--foo 变成一个 flag，语义变了却不报错。
func TestKafkaCommand_RenderRejectsFlagLikeTarget(t *testing.T) {
	cases := []KafkaCommand{
		{Family: "topic", Verb: "describe", Target: "--partitions=3"},
		{Family: "topic", Verb: "describe", Target: "--"},
	}
	for _, c := range cases {
		if _, err := c.Render(); err == nil {
			t.Fatalf("Render(%#v) = nil error, want rejection (target looks like a flag)", c)
		}
	}
}

// TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm 是本 Plan 最要紧的测试。
//
// 它锁住：新 DSL 产出的策略串与今天 7 个 kafka_* 工具产出的**逐字节相同**。
// 不相同的后果不是报错，而是静默——splitKafkaRule 在非 2-token 输入上返回 false，
// MatchKafkaRule 随之返回 false，于是 BuiltinKafkaDangerousDeny 的 deny 规则不再匹配
// 任何东西（fail-open），而 allowlist 侧变成永远 NeedConfirm。两种都不会抛错、不会记日志。
//
// 右侧的 want 值直接抄自现有 Kafka*Command 的实现（internal/ai/helper/kafka_command.go）
// 与内置组（internal/model/entity/policy_group_entity/policy_group.go:305-408），
// 不是"跑一遍看看输出什么"填进来的——那样测试会跟着实现一起错。
func TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{`cluster overview`, "cluster.read *"},
		{`cluster brokers`, "broker.read *"},
		{`cluster broker-config`, "broker.config.read *"},
		{`cluster cluster-configs`, "cluster.config.read *"},
		{`topic list`, "topic.list *"},
		{`topic describe orders`, "topic.read orders"},
		{`topic create orders --partitions=3`, "topic.create orders"},
		{`topic delete orders`, "topic.delete orders"},
		{`topic update-config orders --config=retention.ms=1000`, "topic.config.write orders"},
		{`topic increase-partitions orders --partition-count=6`, "topic.partitions.write orders"},
		{`topic delete-records orders --records='[]'`, "topic.records.delete orders"},
		{`consumer-group list`, "consumer_group.list *"},
		{`consumer-group describe mygroup`, "consumer_group.read mygroup"},
		{`consumer-group reset-offset mygroup --mode=earliest`, "consumer_group.offset.write mygroup"},
		{`consumer-group delete mygroup`, "consumer_group.delete mygroup"},
		{`acl list`, "acl.read *"},
		{`acl create --principal=User:alice`, "acl.write *"},
		{`acl delete --principal=User:alice`, "acl.write *"},
		{`schema list-subjects`, "schema.read *"},
		{`schema list-versions subj`, "schema.read subj"},
		{`schema check-compatibility subj --schema='{}'`, "schema.read subj"},
		{`schema describe subj`, "schema.read subj"},
		{`schema register subj --schema='{}'`, "schema.write subj"},
		{`schema delete subj`, "schema.delete subj"},
		{`connect list-clusters`, "connect.read *"},
		{`connect list-connectors`, "connect.read *"},
		{`connect describe conn`, "connect.read conn"},
		{`connect create conn --config='{}'`, "connect.write conn"},
		{`connect update-config conn --config='{}'`, "connect.write conn"},
		{`connect pause conn`, "connect.state.write conn"},
		{`connect resume conn`, "connect.state.write conn"},
		{`connect restart conn`, "connect.state.write conn"},
		{`connect delete conn`, "connect.delete conn"},
		{`message browse orders`, "message.read orders"},
		{`message inspect orders`, "message.read orders"},
		{`message produce orders --value=x`, "message.write orders"},
	}
	for _, c := range cases {
		parsed, err := ParseKafkaCommand(c.in)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		got, err := parsed.PolicyString()
		if err != nil {
			t.Fatalf("PolicyString() for %q unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("PolicyString() for %q = %q, want %q — a mismatch here silently fails BuiltinKafkaDangerousDeny open",
				c.in, got, c.want)
		}
		if fields := len(strings.Fields(got)); fields != 2 {
			t.Fatalf("PolicyString() for %q = %q has %d fields, want exactly 2 (splitKafkaRule requires it)",
				c.in, got, fields)
		}
	}
}

// TestKafkaCommand_PolicyStringCoversEveryVerb 保证 kafkaVerbs 里没有一个 verb
// 在 PolicyString 这一步才失败：verb 表与 kafka_command.go 各 switch 的 case
// 是两份独立维护的清单，表里多写一个现有函数不认的 verb，症状是命令解析通过、
// 策略串却拿不到——统一 exec 下该操作直接不可达。
func TestKafkaCommand_PolicyStringCoversEveryVerb(t *testing.T) {
	for family, verbs := range kafkaVerbs {
		for verb, needsTarget := range verbs {
			c := &KafkaCommand{Family: family, Verb: verb}
			if needsTarget {
				c.Target = "res"
			}
			got, err := c.PolicyString()
			if err != nil {
				t.Fatalf("PolicyString() for %q %q unexpected error: %v", family, verb, err)
			}
			if fields := len(strings.Fields(got)); fields != 2 {
				t.Fatalf("PolicyString() for %q %q = %q has %d fields, want exactly 2", family, verb, got, fields)
			}
		}
	}
}
