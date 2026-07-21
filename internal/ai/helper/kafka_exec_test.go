package helper

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestCanonicalizeKafkaCommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{`topic describe orders`, "topic.read orders"},
		{`topic create orders --partitions=3 --replication-factor=2`, "topic.create orders"},
		{`message produce orders --value=x`, "message.write orders"},
		{`acl delete --principal=User:alice`, "acl.write *"},
		{`consumer-group reset-offset mygroup --topic=orders --mode=earliest`, "consumer_group.offset.write mygroup"},
	}
	for _, c := range cases {
		got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeKafka}, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalizeKafkaCommand_RejectsBadSyntax(t *testing.T) {
	for _, in := range []string{`nonsense list`, `topic nonsense x`, `topic describe`, `topic list orders`} {
		if got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in); err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, nil error; want rejection", in, got)
		}
	}
}

// TestKafkaFlagsToArgs_NumericFlagsReachTypedRequest 是本任务最要紧的测试：它断言到
// **request 结构体**，而不是断言 args map 里躺着字符串 "3"。
//
// 只断言 map 的话，`--partitions=3` 会因为 aictx.ArgInt64 缺 string 分支而在
// KafkaCreateTopicRequestFromArgs 里变成 0，测试却照样通过——"因为错误的理由通过"。
// browse 那一半更能说明危害：Limit 落成 0 不报错，service 会套用默认条数，
// 用户批准的是取 500 条、拿到的是别的数字，且没有任何错误提示。
func TestKafkaFlagsToArgs_NumericFlagsReachTypedRequest(t *testing.T) {
	c, err := ParseKafkaCommand(`topic create orders --partitions=3 --replication-factor=2`)
	if err != nil {
		t.Fatalf("ParseKafkaCommand unexpected error: %v", err)
	}
	args, err := kafkaFlagsToArgs(c, 42)
	if err != nil {
		t.Fatalf("kafkaFlagsToArgs unexpected error: %v", err)
	}
	if args["asset_id"] != int64(42) {
		t.Fatalf("args[asset_id] = %#v, want int64(42)", args["asset_id"])
	}
	if args["operation"] != "create" {
		t.Fatalf("args[operation] = %#v, want %q", args["operation"], "create")
	}
	if args["topic"] != "orders" {
		t.Fatalf("args[topic] = %#v, want %q", args["topic"], "orders")
	}
	req, err := KafkaCreateTopicRequestFromArgs(42, args)
	if err != nil {
		t.Fatalf("KafkaCreateTopicRequestFromArgs unexpected error: %v", err)
	}
	if req.Partitions != 3 {
		t.Fatalf("CreateTopicRequest.Partitions = %d, want 3", req.Partitions)
	}
	if req.ReplicationFactor != 2 {
		t.Fatalf("CreateTopicRequest.ReplicationFactor = %d, want 2", req.ReplicationFactor)
	}

	browse, err := ParseKafkaCommand(`message browse orders --limit=500 --offset=1000 --partition=2`)
	if err != nil {
		t.Fatalf("ParseKafkaCommand unexpected error: %v", err)
	}
	browseArgs, err := kafkaFlagsToArgs(browse, 42)
	if err != nil {
		t.Fatalf("kafkaFlagsToArgs unexpected error: %v", err)
	}
	breq, err := kafkaBrowseRequestFromArgs(42, browseArgs)
	if err != nil {
		t.Fatalf("kafkaBrowseRequestFromArgs unexpected error: %v", err)
	}
	if breq.Limit != 500 {
		t.Fatalf("BrowseMessagesRequest.Limit = %d, want 500", breq.Limit)
	}
	if breq.Offset != 1000 {
		t.Fatalf("BrowseMessagesRequest.Offset = %d, want 1000", breq.Offset)
	}
	if breq.Partition == nil || *breq.Partition != 2 {
		t.Fatalf("BrowseMessagesRequest.Partition = %v, want 2", breq.Partition)
	}
}

// TestKafkaFlagsToArgs_NormalizesHyphenFlagNames 锁住连字符→下划线：DSL 写
// --replication-factor，而 KafkaCreateTopicRequestFromArgs 读 replication_factor。
// 不转换的后果是静默的——副本因子落成 0，然后 service 报"副本因子必须大于0"，
// 用户已经批准过了。
func TestKafkaFlagsToArgs_NormalizesHyphenFlagNames(t *testing.T) {
	c, err := ParseKafkaCommand(`topic create orders --partitions=3 --replication-factor=2`)
	if err != nil {
		t.Fatalf("ParseKafkaCommand unexpected error: %v", err)
	}
	args, err := kafkaFlagsToArgs(c, 1)
	if err != nil {
		t.Fatalf("kafkaFlagsToArgs unexpected error: %v", err)
	}
	if args["replication_factor"] != "2" {
		t.Fatalf("args[replication_factor] = %#v, want %q", args["replication_factor"], "2")
	}
	if _, ok := args["replication-factor"]; ok {
		t.Fatalf("args still carries the hyphenated key %q, which no *RequestFromArgs reads", "replication-factor")
	}
}

// TestKafkaFlagsToArgs_TargetLandsOnTheKeyHandlersRead 锁住 kafkaTargetArgNames 的每一条：
// 写错一个 key，target 不会报错，而是静默丢失——落到"列出全部 topic"或"操作了默认资源"。
//
// want 值直接抄自 kafka_helper.go 里各 Handle* 实际读的 key，不是跑一遍看输出填的。
func TestKafkaFlagsToArgs_TargetLandsOnTheKeyHandlersRead(t *testing.T) {
	cases := []struct{ in, key, want string }{
		{`topic describe orders`, "topic", "orders"},                 // HandleKafkaTopic: ArgString(args, "topic")
		{`message browse orders`, "topic", "orders"},                 // HandleKafkaMessage: ArgString(args, "topic")
		{`consumer-group describe mygroup`, "group", "mygroup"},      // HandleKafkaConsumerGroup: ArgString(args, "group")
		{`schema describe mysubject`, "subject", "mysubject"},        // HandleKafkaSchema: ArgString(args, "subject")
		{`connect describe myconnector`, "connector", "myconnector"}, // HandleKafkaConnect: ArgString(args, "connector")
	}
	for _, c := range cases {
		parsed, err := ParseKafkaCommand(c.in)
		if err != nil {
			t.Fatalf("ParseKafkaCommand(%q) unexpected error: %v", c.in, err)
		}
		args, err := kafkaFlagsToArgs(parsed, 1)
		if err != nil {
			t.Fatalf("kafkaFlagsToArgs(%q) unexpected error: %v", c.in, err)
		}
		if args[c.key] != c.want {
			t.Fatalf("for %q: args[%q] = %#v, want %q", c.in, c.key, args[c.key], c.want)
		}
	}
}

// TestKafkaFlagsToArgs_TargetMatchesPolicyResource 锁住"被授权的名字 == 被执行的名字"。
//
// 策略串的 resource token 由 Kafka*Command 产出（它对资源名做 TrimSpace），执行侧的
// target 由这里放进 args；两者只要有一处做了对方没做的归一化，用户批准的资源与实际
// 被操作的资源就不是同一个。遍历 kafkaVerbs 而不是抄一张清单：新加的 verb 自动进来。
func TestKafkaFlagsToArgs_TargetMatchesPolicyResource(t *testing.T) {
	for _, family := range slices.Sorted(maps.Keys(kafkaVerbs)) {
		for _, verb := range slices.Sorted(maps.Keys(kafkaVerbs[family])) {
			if !kafkaVerbs[family][verb] {
				continue
			}
			c := &KafkaCommand{Family: family, Verb: verb, Target: "res"}
			policy, err := c.PolicyString()
			if err != nil {
				t.Fatalf("PolicyString() for %q %q unexpected error: %v", family, verb, err)
			}
			args, err := kafkaFlagsToArgs(c, 1)
			if err != nil {
				t.Fatalf("kafkaFlagsToArgs() for %q %q unexpected error: %v", family, verb, err)
			}
			resource := strings.Fields(policy)[1]
			key, ok := kafkaTargetArgNames[family]
			if !ok {
				t.Fatalf("family %q has target-taking verb %q but no entry in kafkaTargetArgNames", family, verb)
			}
			if args[key] != resource {
				t.Fatalf("for %q %q: args[%q] = %#v but policy string authorizes resource %q",
					family, verb, key, args[key], resource)
			}
		}
	}
}

// TestCanonicalizeKafkaCommand_RejectsUnknownFlag 锁住未知 flag 的拒绝。
// `topic create orders --paritions=3`（拼错）在修复前会静默按"分区数 0"建 topic，
// 也就是批准了一件事、执行了另一件——而且 SKILL.md 里列出的 --only-failed /
// --include-tasks 之类"看着像那么回事、其实没人读"的 flag 也属于同一类。
func TestCanonicalizeKafkaCommand_RejectsUnknownFlag(t *testing.T) {
	cases := []string{
		`topic create orders --paritions=3`,
		`topic list --nonsense=1`,
		`message browse orders --group=g`,
		`connect list-connectors --only-failed`,
		`schema register subj --schema='{}' --version=3`,
	}
	for _, in := range cases {
		if got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in); err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, nil error; want rejection (unknown flag)", in, got)
		}
	}
}

// TestCanonicalizeKafkaCommand_UnknownFlagsAreReportedSorted 与 mongo 侧同源的理由：
// parsed.Flags 是 Go map，迭代顺序随机，只报"碰到的第一个"会让同样的输入在不同次调用
// 报出不同的 flag 名。这段文本是发给模型的，不确定性只会让它瞎猜。
func TestCanonicalizeKafkaCommand_UnknownFlagsAreReportedSorted(t *testing.T) {
	const in = `topic create orders --partitions=3 --zzz=1 --aaa=2`
	for range 20 {
		_, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in)
		if err == nil {
			t.Fatal("CanonicalizeKafkaCommand with unknown flags = nil error, want rejection")
		}
		if !strings.Contains(err.Error(), "unknown flag(s) --aaa, --zzz") {
			t.Fatalf("error = %q, want it to list unknown flags sorted as %q", err, "--aaa, --zzz")
		}
	}
}

// TestCanonicalizeKafkaCommand_RejectsSameFlagUnderTwoSpellings：连字符与下划线两种
// 写法归一后是同一个 args key，两个都给的话后写的静默盖掉先写的（谁先谁后取决于 map
// 迭代顺序，同一条命令每次执行的结果可能不同）。cmdline 的重名检查看的是原始拼写，
// 拦不住这一对。
func TestCanonicalizeKafkaCommand_RejectsSameFlagUnderTwoSpellings(t *testing.T) {
	const in = `topic create orders --replication-factor=2 --replication_factor=3`
	for range 20 {
		got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in)
		if err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, nil error; want rejection (one spelling silently wins)", in, got)
		}
		if !strings.Contains(err.Error(), "--replication-factor and --replication_factor") {
			t.Fatalf("error = %q, want it to name both spellings in a stable order", err)
		}
	}
}

// TestCanonicalizeKafkaCommand_RejectsMissingRequiredFlag 锁住"弹窗批准后必然失败"的
// 那一类：这些命令缺了 flag 之后 100% 跑不成——要么 kafka_args.go 的构造器当场报错，
// 要么 kafka_svc 连上集群后报错。拒绝必须发生在 canonicalize 里，也就是权限检查之前，
// 否则用户先被弹一次审批（选 "allow all" 还会落一条常驻 grant），批准完命令才失败。
func TestCanonicalizeKafkaCommand_RejectsMissingRequiredFlag(t *testing.T) {
	cases := []string{
		`message inspect orders --offset=1000`, // 缺 --partition：kafkaInspectRequestFromArgs 报错
		`message inspect orders --partition=0`, // 缺 --offset：同上
		`connect create myconn`,                // 缺 --config：KafkaConnectorConfigRequestFromArgs 报错
		`connect update-config myconn`,         // 同上
		`topic update-config orders`,           // 缺 --config-updates：KafkaAlterTopicConfigRequestFromArgs 报错
		`topic delete-records orders`,          // 缺 --records：KafkaDeleteRecordsRequestFromArgs 报错
	}
	for _, in := range cases {
		if got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in); err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, nil error; want rejection (missing required flag)", in, got)
		}
	}
}

// TestCanonicalizeKafkaCommand_AcceptsDocumentedCommands 是上面几条拒绝的反面守卫：
// 校验收得太紧同样是缺陷（合法命令被拒 = 该操作在统一 exec 下不可达）。用例逐条抄自
// Task 8 要写的 SKILL.md 命令表。
func TestCanonicalizeKafkaCommand_AcceptsDocumentedCommands(t *testing.T) {
	cases := []string{
		`cluster overview`,
		`cluster brokers`,
		`cluster broker-config --broker-id=1`,
		`cluster cluster-configs`,
		`topic list --include-internal --search=orders --page=1 --page-size=50`,
		`topic describe orders`,
		`topic create orders --partitions=3 --replication-factor=2 --configs='{"retention.ms":"604800000"}'`,
		`topic delete orders`,
		`topic update-config orders --config-updates='[{"key":"retention.ms","value":"1000"}]'`,
		`topic increase-partitions orders --partition-count=6`,
		`topic delete-records orders --records='[{"partition":0,"offset":100}]'`,
		`consumer-group list`,
		`consumer-group describe mygroup`,
		`consumer-group reset-offset mygroup --topic=orders --mode=earliest`,
		`consumer-group reset-offset mygroup --topic=orders --mode=offset --offset=1000 --partitions='[0,1]'`,
		`consumer-group reset-offset mygroup --topic=orders --mode=timestamp --timestamp-millis=1700000000000`,
		`consumer-group delete mygroup`,
		`acl list --resource-type=topic --resource-name=orders --principal=User:alice`,
		`acl create --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow --host='*' --pattern-type=literal`,
		`acl delete --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow`,
		`schema list-subjects`,
		`schema list-versions mysubject`,
		`schema describe mysubject --version=3`,
		`schema check-compatibility mysubject --schema='{"type":"record"}'`,
		`schema register mysubject --schema='{"type":"record"}' --schema-type=AVRO`,
		`schema delete mysubject --permanent`,
		`connect list-clusters`,
		`connect list-connectors --cluster=main`,
		`connect describe myconnector --cluster=main`,
		`connect create myconnector --config='{"connector.class":"x"}'`,
		`connect update-config myconnector --config='{"tasks.max":"2"}'`,
		`connect pause myconnector`,
		`connect resume myconnector`,
		`connect restart myconnector --include-tasks --only-failed`,
		`connect delete myconnector`,
		`message browse orders --partition=0 --start-mode=latest --limit=100 --decode-mode=json`,
		`message inspect orders --offset=1000 --partition=0`,
		`message produce orders --value='{"a":1}' --key=k1 --value-encoding=string --headers='[{"key":"h","value":"v"}]'`,
	}
	for _, in := range cases {
		if _, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, in); err != nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = error %v; want acceptance (this command is documented in SKILL.md)", in, err)
		}
	}
}

// TestKafkaFlagRulesCoverEveryVerb 锁住 kafkaFlagRules 与 kafkaVerbs 两张表不分叉。
// 漏一个 verb 的症状是它在统一 exec 下**完全不可达**（缺表项一律拒绝），而 Task 6 的
// PolicyString 覆盖测试查不出来——它只看策略串这一侧。
func TestKafkaFlagRulesCoverEveryVerb(t *testing.T) {
	for family, verbs := range kafkaVerbs {
		rules, ok := kafkaFlagRules[family]
		if !ok {
			t.Fatalf("kafkaFlagRules is missing family %q", family)
		}
		for verb := range verbs {
			if _, ok := rules[verb]; !ok {
				t.Fatalf("kafkaFlagRules[%q] is missing verb %q", family, verb)
			}
		}
		for verb := range rules {
			if _, ok := verbs[verb]; !ok {
				t.Fatalf("kafkaFlagRules[%q] has verb %q that kafkaVerbs does not know", family, verb)
			}
		}
	}
	for family := range kafkaFlagRules {
		if _, ok := kafkaVerbs[family]; !ok {
			t.Fatalf("kafkaFlagRules has family %q that kafkaVerbs does not know", family)
		}
	}
}

// TestKafkaFlagRulesDoNotShadowTargetArg 锁住 flag 与 target 不抢同一个 args key。
// kafkaFlagsToArgs 后写 target，所以冲突的表现不是报错而是"用户写的 flag 被吃掉"；
// 反过来若某天改成先写 target，就变成 target 被吃掉。两种都静默，直接在表这一层禁掉。
func TestKafkaFlagRulesDoNotShadowTargetArg(t *testing.T) {
	for family, verbs := range kafkaFlagRules {
		key, ok := kafkaTargetArgNames[family]
		if !ok {
			continue
		}
		for verb, spec := range verbs {
			for _, name := range slices.Concat(spec.required, spec.optional) {
				if normalizeKafkaFlagName(name) == key {
					t.Fatalf("%q %q allows flag --%s, which collides with the target arg key %q",
						family, verb, name, key)
				}
			}
		}
	}
}
