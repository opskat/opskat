package helper

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/policy"
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

// TestParseKafkaCommand_RejectsWhitespaceInTarget 锁住 fail-open 修复：策略串是
// "<action> <resource>" 恰好两个 whitespace 分隔 token，target 里带空白会让
// splitKafkaRule 切出 3 段、返回 ok=false，MatchKafkaRule 对**任何**规则都静默返回
// false —— 实测 `consumer-group delete 'my group'` 在 BuiltinKafkaDangerousDeny 下
// 从 Deny 掉成 NeedConfirm。
//
// NBSP(U+00A0) / U+3000 单列且不加引号：shell 切词不认它们（所以它们能不带引号
// 直接走进 Target），而 strings.Fields 的切分谓词 unicode.IsSpace 认得——这个错位
// 正是漏洞本体，中文输入法下完全够得着。
func TestParseKafkaCommand_RejectsWhitespaceInTarget(t *testing.T) {
	cases := []string{
		`consumer-group delete 'my group'`,
		"consumer-group delete my\u00a0group",
		"consumer-group delete 'my\u00a0group'",
		"consumer-group delete my\u3000group",
		`schema describe 'sub ject'`,
		`connect describe 'my conn'`,
		"schema describe 'a\tb'",
		`topic delete ' orders '`,
	}
	for _, in := range cases {
		if _, err := ParseKafkaCommand(in); err == nil {
			t.Fatalf("ParseKafkaCommand(%q) = nil error, want rejection (whitespace in target breaks the 2-token policy string)", in)
		}
	}
}

// TestParseKafkaCommand_RejectsTargetForVerbThatTakesNone 锁住 needsTarget 的**上界**。
// `topic list orders`（模型想说 "describe orders" 却写成 list）在修复前解析成功、审批
// 弹窗照原样显示 `topic list orders`，然后执行器列出集群里全部 topic —— 批准一件事、
// 拿到另一件。ParseMongoCommand 早就有同形的守卫。
func TestParseKafkaCommand_RejectsTargetForVerbThatTakesNone(t *testing.T) {
	cases := []string{
		`topic list orders`,
		`connect list-connectors prod-cluster`,
		`cluster brokers broker-1`,
		`schema list-subjects mysubject`,
		`consumer-group list mygroup`,
		`acl list mytopic`,
	}
	for _, in := range cases {
		if _, err := ParseKafkaCommand(in); err == nil {
			t.Fatalf("ParseKafkaCommand(%q) = nil error, want rejection (verb takes no target)", in)
		}
	}
}

// TestParseKafkaCommand_RejectsMissingTarget 锁住 needsTarget 的下界。
// 今天"看起来不出事"只是巧合：每个 needsTarget 的 verb 对应的 Kafka*Command 自己也拒空
// 资源名。哪天某个策略函数不再拒空，这一层没拦截就变成静默放行。
func TestParseKafkaCommand_RejectsMissingTarget(t *testing.T) {
	cases := []string{
		`topic describe`,
		`topic delete`,
		`consumer-group reset-offset`,
		`schema register`,
		`connect restart`,
		`message produce`,
	}
	for _, in := range cases {
		if _, err := ParseKafkaCommand(in); err == nil {
			t.Fatalf("ParseKafkaCommand(%q) = nil error, want rejection (verb requires a target)", in)
		}
	}
}

// TestParseKafkaCommand_RejectsExtraPositional 锁住 `<family> <verb> [target]` 之外的
// 多余 positional：不拒绝就会被静默丢弃，而审批文本里还留着它。
func TestParseKafkaCommand_RejectsExtraPositional(t *testing.T) {
	cases := []string{
		`topic describe orders extra`,
		`message produce orders t1 t2`,
		`topic list a b`,
	}
	for _, in := range cases {
		if _, err := ParseKafkaCommand(in); err == nil {
			t.Fatalf("ParseKafkaCommand(%q) = nil error, want rejection (too many positional arguments)", in)
		}
	}
}

// TestKafkaCommand_PolicyStringRejectsNonTwoTokenResult 覆盖 ParseKafkaCommand 够不到
// 的那条路：KafkaCommand 是导出结构体，统一 exec 会从结构化工具参数直接构造它
// （opsctl 的 mongo 子命令就是这么干的），完全绕开解析期的空白校验。
// PolicyString 的后置条件是这条缝上唯一的守卫，而这条缝破了是**静默**的。
func TestKafkaCommand_PolicyStringRejectsNonTwoTokenResult(t *testing.T) {
	cases := []KafkaCommand{
		{Family: "consumer-group", Verb: "delete", Target: "my group"},
		{Family: "consumer-group", Verb: "delete", Target: "my\u00a0group"},
		{Family: "schema", Verb: "delete", Target: "a\u3000b"},
		{Family: "connect", Verb: "delete", Target: "a\tb"},
	}
	for _, c := range cases {
		got, err := c.PolicyString()
		if err == nil {
			t.Fatalf("PolicyString(%#v) = %q, nil error; want rejection (result is not exactly 2 fields)", c, got)
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

// TestKafkaCommand_RoundTripNormalizesNilFlags 锁住 KafkaCommand.Flags 的 nil 语义：
// 手工构造（统一 exec 的结构化参数路径）允许 Flags 为 nil，但 ParseKafkaCommand 永远
// 返回非 nil map，所以 round-trip 回来的是空 map 而不是 nil，reflect.DeepEqual 判不相等。
// 这是有意的：nil 与空 map 在读取侧完全等价，与其在 Render 里加一层归一化，不如把它
// 写成契约测下来——否则下游会以"round-trip 挂了"的形式撞见它。
func TestKafkaCommand_RoundTripNormalizesNilFlags(t *testing.T) {
	src := KafkaCommand{Family: "topic", Verb: "describe", Target: "orders"}
	rendered, err := src.Render()
	if err != nil {
		t.Fatalf("Render(%#v) unexpected error: %v", src, err)
	}
	got, err := ParseKafkaCommand(rendered)
	if err != nil {
		t.Fatalf("ParseKafkaCommand(%q) unexpected error: %v", rendered, err)
	}
	want := KafkaCommand{Family: "topic", Verb: "describe", Target: "orders", Flags: map[string]string{}}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round-trip of nil-Flags command = %#v, want %#v", *got, want)
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

// TestKafkaCommand_RenderRejectsUnsafeFlagName 同上一条的理由，只是换到 flag 名这一维：
// cmdline.Command.Render 的文档明说手写字面量不受 Parse 的入口校验保护、由调用方保证
// flag 名合法——KafkaCommand.Render 就是那个调用方。mongo 靠写死两个 flag 名绕开了这件事，
// kafka 接受任意 flag 名，绕不开。`cfg=x` 渲染成 --cfg=x=1，重新解析后变成 flag "cfg"
// 值 "x=1"，是静默的数据损坏。
func TestKafkaCommand_RenderRejectsUnsafeFlagName(t *testing.T) {
	cases := []KafkaCommand{
		{Family: "topic", Verb: "create", Target: "orders", Flags: map[string]string{"cfg=x": "1"}},
		{Family: "topic", Verb: "create", Target: "orders", Flags: map[string]string{"a b": "1"}},
		{Family: "topic", Verb: "create", Target: "orders", Flags: map[string]string{"": "1"}},
		{Family: "topic", Verb: "create", Target: "orders", Flags: map[string]string{"a;rm -rf": "1"}},
	}
	for _, c := range cases {
		if got, err := c.Render(); err == nil {
			t.Fatalf("Render(%#v) = %q, nil error; want rejection (flag name does not survive re-parsing)", c, got)
		}
	}
}

// TestKafkaCommand_PolicyStringIsUnchangedTwoTokenForm 是本 Plan 最要紧的测试。
//
// 它锁住：新 DSL 产出的策略串与已删除的 7 个 kafka_* 工具产出的**逐字节相同**——
// 工具没了，但用户库里按那个形式写下的 CmdPolicy 与 grant 还在，形式变了就全部失配。
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

// kafkaActionIsMutating 按策略串 action 的**末段**判定读还是写：
// "topic.records.delete" → delete → 写，"broker.config.read" → read → 读。
//
// 词表是封闭的，出现没见过的末段就 fail：新末段意味着有人加了一类操作，它算读还是算写
// 必须由人来定。给个默认值猜的话，猜成"读"就等于悄悄把它移出下面那条覆盖检查——
// 一个漏掉 deny 覆盖的写操作，正好从此不会被任何测试提起。
var kafkaActionIsMutating = map[string]bool{
	"read": false, "list": false,
	"create": true, "write": true, "delete": true,
}

// TestKafkaBuiltinDeny_CoversEveryMutatingFamily 从 kafkaVerbs 推导"哪些 family 有写操作"，
// 再用默认生效的 deny 清单（EffectiveKafkaPolicy(nil) 展开 BuiltinKafkaDangerousDeny）逐条
// 匹配，把"有写 verb、却一条内置 deny 规则都不沾"的 family 钉成一个已知集合。
//
// 两张表各自维护、都会漂，而漂的症状是静默的：给 cluster 加一个写 verb、或把
// "acl.write *" 从内置清单里删掉，今天不会让任何测试变红——只是危险操作默认不再被拦，
// 也就是本分支已经出过五次的 fail-open 形状。
//
// 粒度是 family 不是 verb：topic.create 本来就不在 deny 清单里（内置 Kafka Operator 组
// 明确允许建 topic），逐 verb 断言会把这类有意的放行也算成缺口。family 级别问的是
// "这个 family 产出的策略串到底能不能被内置 deny 命中"。
//
// message 在 want 里，是一条真实的缺口而非笔误：message produce 产出
// "message.write <topic>"，是往生产集群写数据的操作，BuiltinKafkaDangerousDeny 里却没有
// 对应规则（内置 Kafka Operator 组反倒把 "message.write *" 列进了 allow）。默认策略下
// 它落到 NeedConfirm——弹审批而不是直接拒绝。这是既有的产品判断，本测试只负责让它
// 不再是无人知晓的状态；tool 包的 TestHandleExec_KafkaDenyListStopsBeforeConnecting
// 用自定义策略覆盖了这条唯一能拦住它的路径。
func TestKafkaBuiltinDeny_CoversEveryMutatingFamily(t *testing.T) {
	denyRules := policy.EffectiveKafkaPolicy(context.Background(), nil).DenyList
	if len(denyRules) == 0 {
		t.Fatal("default kafka policy has an empty deny list — BuiltinKafkaDangerousDeny is not being resolved")
	}

	mutating := map[string]bool{}
	denied := map[string]bool{}
	for family, verbs := range kafkaVerbs {
		for verb, needsTarget := range verbs {
			c := &KafkaCommand{Family: family, Verb: verb}
			if needsTarget {
				c.Target = "res"
			}
			command, err := c.PolicyString()
			if err != nil {
				t.Fatalf("PolicyString() for %q %q unexpected error: %v", family, verb, err)
			}
			action, _, _ := strings.Cut(command, " ")
			kind := action[strings.LastIndex(action, ".")+1:]
			isMutating, known := kafkaActionIsMutating[kind]
			if !known {
				t.Fatalf("policy action %q (from %q %q) ends in %q, which is not in kafkaActionIsMutating — classify it as read or write before adding it",
					action, family, verb, kind)
			}
			if !isMutating {
				continue
			}
			mutating[family] = true
			for _, rule := range denyRules {
				if policy.MatchKafkaRule(rule, command) {
					denied[family] = true
				}
			}
		}
	}

	var uncovered []string
	for family := range mutating {
		if !denied[family] {
			uncovered = append(uncovered, family)
		}
	}
	slices.Sort(uncovered)

	want := []string{"message"}
	if !slices.Equal(uncovered, want) {
		t.Fatalf("families with write verbs but no builtin deny rule = %v, want %v — a new one means a destructive operation ships un-denied by default; one disappearing means the finding was fixed and this expectation must shrink",
			uncovered, want)
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
