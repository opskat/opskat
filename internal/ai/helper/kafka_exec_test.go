package helper

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/kafka_svc"
)

func TestCanonicalizeKafkaCommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{`topic describe orders`, "topic.read orders"},
		{`topic create orders --partitions=3 --replication-factor=2`, "topic.create orders"},
		{`message produce orders --value=x`, "message.write orders"},
		{`acl delete --resource-type=topic --principal=User:alice --acl-operation=read --permission=allow --host='*'`, "acl.write *"},
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
	// want 是错误文本的前缀，用来确认**是必填检查**拒的它，而不是别的检查恰好先开火
	// ——后者与"没有这条测试"等价。
	cases := []struct{ in, want string }{
		// 构造器当场报错的一组（kafka_args.go）
		{`message inspect orders --offset=1000`, `"message" "inspect" requires --partition`},
		{`message inspect orders --partition=0`, `"message" "inspect" requires --offset`},
		{`connect create myconn`, `"connect" "create" requires --config`},
		{`connect update-config myconn`, `"connect" "update-config" requires --config`},
		{`topic update-config orders`, `"topic" "update-config" requires --config-updates`},
		{`topic delete-records orders`, `"topic" "delete-records" requires --records`},
		// kafka_svc 里**无条件**报错的一组：不拦的话用户会先被弹审批、再白连一次集群
		{`topic create orders`, `"topic" "create" requires --partitions, --replication-factor`},
		{`topic create orders --partitions=3`, `"topic" "create" requires --replication-factor`},
		{`topic increase-partitions orders`, `"topic" "increase-partitions" requires --partition-count`},
		{`schema register subj`, `"schema" "register" requires --schema`},
		{`schema check-compatibility subj`, `"schema" "check-compatibility" requires --schema`},
		{`consumer-group reset-offset g --mode=earliest`, `"consumer-group" "reset-offset" requires --topic`},
		{`acl create --principal=User:alice`, `"acl" "create" requires --acl-operation, --permission, --resource-type`},
		{`acl delete --principal=User:alice`, `"acl" "delete" requires --acl-operation, --host, --permission, --resource-type`},
		// 显式给了空值 == 没给：--config-updates='' 之后构造器照样报 "is required"。
		// 只判 key 是否存在（而不是值是否非空）会放它过去。
		{`topic update-config orders --config-updates=''`, `"topic" "update-config" requires --config-updates`},
		{`connect create myconn --config=''`, `"connect" "create" requires --config`},
		{`schema register subj --schema='  '`, `"schema" "register" requires --schema`},
	}
	for _, c := range cases {
		got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, c.in)
		if err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, nil error; want rejection (missing required flag)", c.in, got)
		}
		if !strings.HasPrefix(err.Error(), c.want) {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = error %q, want it to start with %q (a different check firing first is not this test passing)",
				c.in, err, c.want)
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
		// 注意是 "name" 不是 "key"：TopicConfigMutation 的 JSON 字段叫 name，写 key
		// 会被 encoding/json 静默丢弃。Plan 的 SKILL.md 草稿这里写错了。
		`topic update-config orders --config-updates='[{"name":"retention.ms","value":"1000"}]'`,
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
		// --host 是 acl delete 才有的必填项（acl_admin.go:204 无条件拒空），Plan 的
		// SKILL.md 草稿漏了它——照抄会得到一条必然失败的示例命令。
		`acl delete --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow --host='*'`,
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

// TestCanonicalizeKafkaCommand_RejectsMalformedFlagValue 是本轮修复的核心：
// flag **给了**，但值的形状不对。这些今天全都能一路走到审批弹窗，批准之后才失败
// （构造器报错），或者更糟——不失败，被静默当成 0 / 默认值：
// `--limit=1,000` 实测拿到 50 条（messages.go:174 的默认值），用户批准的是 1000 条。
//
// 断言故意用 "invalid value for --x" 前缀而不是 err != nil：这一层还有"未知 flag"
// 和"缺必填 flag"两条更早的拒绝，只断言"有错"的话，哪天形状检查被删掉，测试可能靠
// 另一条检查误杀而继续通过——那和没有测试是一回事。
func TestCanonicalizeKafkaCommand_RejectsMalformedFlagValue(t *testing.T) {
	cases := []struct{ in, flag string }{
		// FIX C：构造器当场就会报错，都是"批准后必然失败"
		{`topic update-config orders --config-updates=notjson`, "config-updates"},
		{`topic update-config orders --config-updates='[{"key":"retention.ms","value":"1"}]'`, "config-updates"},
		{`topic delete-records orders --records=notjson`, "records"},
		{`connect create myconn --config=notjson`, "config"},
		{`connect create myconn --config='{}'`, "config"},
		{`connect create myconn --config`, "config"},
		{`connect update-config myconn --config='{}'`, "config"},
		{`message inspect orders --partition=abc --offset=1`, "partition"},
		{`consumer-group reset-offset g --topic=t --partitions=notjson`, "partitions"},
		{`schema register subj --schema='{}' --references=notjson`, "references"},
		{`schema check-compatibility subj --schema='{}' --references=notjson`, "references"},
		{`message produce orders --value=x --headers=notjson`, "headers"},
		{`topic create orders --partitions=3 --replication-factor=2 --configs=notjson`, "configs"},
		// FIX D：数值 flag 解析不出来 → ArgInt64 给 0 → service 套默认值，**不报错**
		{`message browse orders --limit=1,000`, "limit"},
		{`message browse orders --limit=1_000`, "limit"},
		{`message browse orders --limit=1e3`, "limit"},
		{`message browse orders --limit=abc`, "limit"},
		{`message browse orders --max-bytes=1kb`, "max-bytes"},
		{`topic create orders --partitions=3.0 --replication-factor=2`, "partitions"},
		{`topic list --page=99999999999999999999`, "page"},
		{`cluster broker-config --broker-id=one`, "broker-id"},
		{`topic increase-partitions orders --partition-count=six`, "partition-count"},
		// 布尔 flag：ArgBool 把任何非 "true" 的值都读成 false
		{`schema delete subj --permanent=yes`, "permanent"},
		{`topic list --include-internal=1`, "include-internal"},
		{`connect restart conn --only-failed=maybe`, "only-failed"},
	}
	for _, c := range cases {
		got, err := CanonicalizeKafkaCommand(&asset_entity.Asset{ID: 1}, c.in)
		if err == nil {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = %q, nil error; want rejection (malformed value)", c.in, got)
		}
		if want := "invalid value for --" + c.flag; !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("CanonicalizeKafkaCommand(%q) = error %q, want it to start with %q (a different check firing first is not this test passing)",
				c.in, err, want)
		}
	}
}

// TestKafkaFlagsToArgs_OperationMatchesHandlerSwitch 钉住 verb→operation 的还原。
//
// 11 个 verb 的 DSL 写法与 Handle* switch 里的 operation 值不同（连字符 vs 下划线）。
// 把 c.operation() 换成 c.Verb，那 11 个操作全部落到各 Handle* 的 default 分支、
// 报 "unsupported kafka_X operation" —— 又一个"审批之后才失败"。此前整个测试套件
// 唯一断言 operation 的地方用的是 "create"（一个**不需要**映射的 verb），
// 所以那个变异是全绿的。
func TestKafkaFlagsToArgs_OperationMatchesHandlerSwitch(t *testing.T) {
	// want 逐条抄自 kafka_helper.go 各 Handle* 的 case 标签，不是跑一遍看输出填的。
	want := map[string]map[string]string{
		"cluster": {
			"overview": "overview", "brokers": "brokers",
			"broker-config": "get_broker_config", "cluster-configs": "list_cluster_configs",
		},
		"topic": {
			"list": "list", "describe": "describe", "create": "create", "delete": "delete",
			"update-config": "update_config", "increase-partitions": "increase_partitions",
			"delete-records": "delete_records",
		},
		"consumer-group": {
			"list": "list", "describe": "describe", "reset-offset": "reset_offset", "delete": "delete",
		},
		"acl": {"list": "list", "create": "create", "delete": "delete"},
		"schema": {
			"list-subjects": "list_subjects", "list-versions": "list_versions",
			"describe": "describe", "check-compatibility": "check_compatibility",
			"register": "register", "delete": "delete",
		},
		"connect": {
			"list-clusters": "list_clusters", "list-connectors": "list_connectors",
			"describe": "describe", "create": "create", "update-config": "update_config",
			"pause": "pause", "resume": "resume", "restart": "restart", "delete": "delete",
		},
		"message": {"browse": "browse", "inspect": "inspect", "produce": "produce"},
	}

	for family, verbs := range kafkaVerbs {
		for verb, needsTarget := range verbs {
			wantOp, ok := want[family][verb]
			if !ok {
				t.Fatalf("kafkaVerbs has %q %q but this test has no expected operation for it", family, verb)
			}
			c := &KafkaCommand{Family: family, Verb: verb}
			if needsTarget {
				c.Target = "res"
			}
			args, err := kafkaFlagsToArgs(c, 1)
			if err != nil {
				t.Fatalf("kafkaFlagsToArgs() for %q %q unexpected error: %v", family, verb, err)
			}
			if args["operation"] != wantOp {
				t.Fatalf("args[operation] for %q %q = %#v, want %q — this value is what each Handle* switches on",
					family, verb, args["operation"], wantOp)
			}
		}
	}
	for family, verbs := range want {
		for verb := range verbs {
			if _, ok := kafkaVerbs[family][verb]; !ok {
				t.Fatalf("this test expects %q %q but kafkaVerbs does not know it", family, verb)
			}
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
			for _, name := range slices.Concat(
				slices.Collect(maps.Keys(spec.required)), slices.Collect(maps.Keys(spec.optional))) {
				if normalizeKafkaFlagName(name) == key {
					t.Fatalf("%q %q allows flag --%s, which collides with the target arg key %q",
						family, verb, name, key)
				}
			}
		}
	}
}

// kafkaFlagCoverageCase 是"每个合法 flag 都真的落到 typed request 的某个字段上"的用例。
//
// build 优先用 kafka_args.go 里真正的构造器；少数几个 verb 的 args→request 映射是写在
// HandleKafka* 里的结构体字面量（没有构造器可复用），那几条在注释里标了镜像自哪一行。
type kafkaFlagCoverageCase struct {
	family, verb string
	// flags 必须与 kafkaFlagRules 里该 verb 的 flag 集合**完全一致**（测试会双向断言）：
	// 表里加一个没人读的 flag，这里就得跟着加，然后"去掉它 request 不变"会失败；
	// 表里删掉一个真 flag，这里也得跟着删，然后"字段非零"会失败。两个漂移方向各有一条死路。
	flags  map[string]string
	build  func(args map[string]any) (any, error)
	zeroOK []string // 不由 flag 决定、允许保持零值的字段
}

var kafkaFlagCoverageCases = []kafkaFlagCoverageCase{
	{
		family: "cluster", verb: "broker-config",
		flags: map[string]string{"broker-id": "3"},
		// 镜像 kafka_helper.go:77（HandleKafkaCluster 的 get_broker_config 分支）
		build: func(args map[string]any) (any, error) {
			return struct{ BrokerID int32 }{int32(aictx.ArgInt64(args, "broker_id"))}, nil
		},
	},
	{
		family: "topic", verb: "list",
		flags: map[string]string{"include-internal": "true", "search": "ord", "page": "2", "page-size": "10"},
		// 镜像 kafka_helper.go:113-119（HandleKafkaTopic 的 list 分支）
		build: func(args map[string]any) (any, error) {
			return kafka_svc.ListTopicsRequest{
				AssetID:         aictx.ArgInt64(args, "asset_id"),
				IncludeInternal: aictx.ArgBool(args, "include_internal"),
				Search:          strings.TrimSpace(aictx.ArgString(args, "search")),
				Page:            aictx.ArgInt(args, "page"),
				PageSize:        aictx.ArgInt(args, "page_size"),
			}, nil
		},
	},
	{
		family: "topic", verb: "create",
		flags: map[string]string{"partitions": "3", "replication-factor": "2", "configs": `{"retention.ms":"1000"}`},
		build: func(args map[string]any) (any, error) {
			return KafkaCreateTopicRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "topic", verb: "update-config",
		flags: map[string]string{"config-updates": `[{"name":"retention.ms","value":"1000"}]`},
		build: func(args map[string]any) (any, error) {
			return KafkaAlterTopicConfigRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "topic", verb: "increase-partitions",
		flags: map[string]string{"partition-count": "6"},
		build: func(args map[string]any) (any, error) {
			return kafkaIncreasePartitionsRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args), nil
		},
	},
	{
		family: "topic", verb: "delete-records",
		flags: map[string]string{"records": `[{"partition":0,"offset":100}]`},
		build: func(args map[string]any) (any, error) {
			return KafkaDeleteRecordsRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "consumer-group", verb: "reset-offset",
		flags: map[string]string{
			"topic": "orders", "partitions": "[0,1]", "mode": "offset",
			"offset": "100", "timestamp-millis": "1700000000000",
		},
		build: func(args map[string]any) (any, error) {
			return KafkaResetConsumerGroupOffsetRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "acl", verb: "list",
		flags: map[string]string{
			"resource-type": "topic", "resource-name": "orders", "pattern-type": "literal",
			"principal": "User:alice", "host": "*", "acl-operation": "read",
			"permission": "allow", "page": "2", "page-size": "10",
		},
		build: func(args map[string]any) (any, error) {
			return KafkaListACLsRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args), nil
		},
	},
	{
		family: "acl", verb: "create",
		flags: map[string]string{
			"resource-type": "topic", "resource-name": "orders", "pattern-type": "literal",
			"principal": "User:alice", "host": "*", "acl-operation": "read", "permission": "allow",
		},
		build: func(args map[string]any) (any, error) {
			return KafkaCreateACLRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args), nil
		},
	},
	{
		family: "acl", verb: "delete",
		flags: map[string]string{
			"resource-type": "topic", "resource-name": "orders", "pattern-type": "literal",
			"principal": "User:alice", "host": "*", "acl-operation": "read", "permission": "allow",
		},
		build: func(args map[string]any) (any, error) {
			return KafkaDeleteACLRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args), nil
		},
	},
	{
		family: "schema", verb: "describe",
		flags: map[string]string{"version": "3"},
		// 镜像 kafka_helper.go:302（HandleKafkaSchema 的 get/describe 分支）
		build: func(args map[string]any) (any, error) {
			return struct{ Version string }{aictx.ArgString(args, "version")}, nil
		},
	},
	{
		family: "schema", verb: "check-compatibility",
		flags: map[string]string{
			"version": "3", "schema": "{}", "schema-type": "AVRO",
			"references": `[{"name":"n","subject":"s","version":1}]`,
		},
		build: func(args map[string]any) (any, error) {
			return KafkaCheckSchemaCompatibilityRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "schema", verb: "register",
		flags: map[string]string{
			"schema": "{}", "schema-type": "AVRO",
			"references": `[{"name":"n","subject":"s","version":1}]`,
		},
		build: func(args map[string]any) (any, error) {
			return KafkaRegisterSchemaRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "schema", verb: "delete",
		flags: map[string]string{"version": "3", "permanent": "true"},
		build: func(args map[string]any) (any, error) {
			return KafkaDeleteSchemaRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args), nil
		},
	},
	{
		family: "connect", verb: "list-connectors",
		flags: map[string]string{"cluster": "main"},
		// 镜像 kafka_helper.go:355+364（cluster 对所有 connect 分支是同一个读法）
		build: func(args map[string]any) (any, error) { return kafkaConnectClusterOnly(args), nil },
	},
	{
		family: "connect", verb: "describe",
		flags: map[string]string{"cluster": "main"},
		build: func(args map[string]any) (any, error) { return kafkaConnectClusterOnly(args), nil },
	},
	{
		family: "connect", verb: "pause",
		flags: map[string]string{"cluster": "main"},
		build: func(args map[string]any) (any, error) { return kafkaConnectClusterOnly(args), nil },
	},
	{
		family: "connect", verb: "resume",
		flags: map[string]string{"cluster": "main"},
		build: func(args map[string]any) (any, error) { return kafkaConnectClusterOnly(args), nil },
	},
	{
		family: "connect", verb: "delete",
		flags: map[string]string{"cluster": "main"},
		build: func(args map[string]any) (any, error) { return kafkaConnectClusterOnly(args), nil },
	},
	{
		family: "connect", verb: "create",
		flags: map[string]string{"cluster": "main", "config": `{"tasks.max":"2"}`},
		build: func(args map[string]any) (any, error) {
			return KafkaConnectorConfigRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "connect", verb: "update-config",
		flags: map[string]string{"cluster": "main", "config": `{"tasks.max":"2"}`},
		build: func(args map[string]any) (any, error) {
			return KafkaConnectorConfigRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "connect", verb: "restart",
		flags: map[string]string{"cluster": "main", "include-tasks": "true", "only-failed": "true"},
		build: func(args map[string]any) (any, error) {
			return KafkaRestartConnectorRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args), nil
		},
	},
	{
		family: "message", verb: "browse",
		flags: map[string]string{
			"partition": "1", "start-mode": "offset", "offset": "100",
			"timestamp-millis": "1700000000000", "limit": "10", "max-bytes": "1024",
			"decode-mode": "json", "max-wait-millis": "500",
		},
		build: func(args map[string]any) (any, error) {
			return kafkaBrowseRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
	{
		family: "message", verb: "inspect",
		flags: map[string]string{
			"partition": "1", "offset": "100", "max-bytes": "1024",
			"decode-mode": "json", "max-wait-millis": "500",
		},
		build: func(args map[string]any) (any, error) {
			return kafkaInspectRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
		// inspect 不读 timestamp_millis：它把 StartMode 写死成 "offset"（kafka_args.go:274）。
		zeroOK: []string{"TimestampMillis"},
	},
	{
		family: "message", verb: "produce",
		flags: map[string]string{
			"partition": "1", "key": "k", "key-encoding": "string", "value": "v",
			"value-encoding": "string", "headers": `[{"key":"h","value":"v"}]`,
			"timestamp-millis": "1700000000000",
		},
		build: func(args map[string]any) (any, error) {
			return kafkaProduceRequestFromArgs(aictx.ArgInt64(args, "asset_id"), args)
		},
	},
}

// kafkaConnectClusterOnly 镜像 kafka_helper.go:355 —— 只带 --cluster 的那几个 connect 分支
// 在 handler 里就是这一个 ArgString。
func kafkaConnectClusterOnly(args map[string]any) any {
	return struct{ Cluster string }{aictx.ArgString(args, "cluster")}
}

// TestKafkaFlagRules_EveryFlagReachesItsRequestField 把 kafkaFlagRules 与"真正被读取的
// 参数"这两处真相钉在一起。表是第二处真相，两个漂移方向都是静默的：
//
//	多写一个没人读的 flag  → 用户以为设置生效了，其实被丢弃（批准 X、执行默认值）
//	少写一个真 flag        → 该参数在统一 exec 下不可达，而模型和用户都不知道
//
// 三条断言分别封死：flag 集合双向一致 / 全量 flag 下 request 无零值字段 / 去掉任一
// flag 都能观察到 request 变化。
func TestKafkaFlagRules_EveryFlagReachesItsRequestField(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range kafkaFlagCoverageCases {
		key := c.family + " " + c.verb
		if covered[key] {
			t.Fatalf("duplicate coverage case for %q", key)
		}
		covered[key] = true

		spec, ok := kafkaFlagRules[c.family][c.verb]
		if !ok {
			t.Fatalf("coverage case %q has no entry in kafkaFlagRules", key)
		}
		wantFlags := slices.Sorted(slices.Values(slices.Concat(
			slices.Collect(maps.Keys(spec.required)), slices.Collect(maps.Keys(spec.optional)))))
		gotFlags := slices.Sorted(maps.Keys(c.flags))
		if !slices.Equal(gotFlags, wantFlags) {
			t.Fatalf("coverage case %q covers flags %v, but kafkaFlagRules declares %v", key, gotFlags, wantFlags)
		}

		full, err := kafkaCoverageRequest(t, c, c.flags)
		if err != nil {
			t.Fatalf("%q: building the request with every flag set failed: %v", key, err)
		}

		// 每个导出字段都必须被填上。少写一个 flag 会让对应字段停在零值上。
		v := reflect.ValueOf(full)
		for i := range v.NumField() {
			name := v.Type().Field(i).Name
			if slices.Contains(c.zeroOK, name) {
				continue
			}
			if v.Field(i).IsZero() {
				t.Fatalf("%q: request field %s is still zero after setting every flag in kafkaFlagRules — "+
					"either a flag is missing from the table or the field is unreachable via exec", key, name)
			}
		}

		// 去掉任一 flag 都必须能观察到差异，否则这个 flag 根本没人读。
		for name := range c.flags {
			reduced := maps.Clone(c.flags)
			delete(reduced, name)
			got, err := kafkaCoverageRequest(t, c, reduced)
			if err != nil {
				continue // 构造器直接报错也是可观察的差异
			}
			if reflect.DeepEqual(full, got) {
				t.Fatalf("%q: dropping --%s does not change the typed request — nothing reads this flag, "+
					"so approving a command containing it silently executes the default", key, name)
			}
		}
	}

	for family, verbs := range kafkaFlagRules {
		for verb, spec := range verbs {
			if len(spec.required)+len(spec.optional) == 0 {
				continue
			}
			if !covered[family+" "+verb] {
				t.Fatalf("%q %q declares flags but has no kafkaFlagCoverageCase", family, verb)
			}
		}
	}
}

func kafkaCoverageRequest(t *testing.T, c kafkaFlagCoverageCase, flags map[string]string) (any, error) {
	t.Helper()
	cmd := &KafkaCommand{Family: c.family, Verb: c.verb, Flags: flags}
	if kafkaVerbs[c.family][c.verb] {
		cmd.Target = "res"
	}
	args, err := kafkaFlagsToArgs(cmd, 1)
	if err != nil {
		t.Fatalf("kafkaFlagsToArgs for %q %q: %v", c.family, c.verb, err)
	}
	return c.build(args)
}

// TestExecKafkaOnAsset_DispatchesToItsOwnFamilyHandler 钉住 7 路分派。
//
// 把 `case "topic"` 接到 HandleKafkaConsumerGroup 上，整个测试套件此前是全绿的——
// 而它在生产里意味着"删 topic"的请求走进了 consumer group 处理器：同一个资产上
// **另一类资源**被操作，或者更常见地，静默失败在一个看不懂的错误上。
//
// 观察点选权限检查而不是 service 调用：每个 Handle* 在碰服务之前都会先算出**自己家族的**
// 策略串交给 checker（kafka_helper.go 各函数开头），所以 checker 收到的串就是"哪个
// handler 真的跑了"的指纹；回调返回 deny，函数在此返回，全程不连集群。
// 这也顺带证明了 ExecKafkaOnAsset → Handle* → 权限检查这条链是通的。
func TestExecKafkaOnAsset_DispatchesToItsOwnFamilyHandler(t *testing.T) {
	// 每条都特意避开 BuiltinKafkaDangerousDeny 的 deny 列表（topic.delete /
	// consumer_group.delete / acl.write / schema.delete / connect.delete …）：
	// 命中 deny 时 checker 直接返回，回调根本不触发，断言就成了空转。
	cases := []struct{ command, wantPolicyCommand string }{
		{`cluster overview`, "cluster.read *"},
		{`topic describe orders`, "topic.read orders"},
		{`consumer-group describe mygroup`, "consumer_group.read mygroup"},
		{`acl list`, "acl.read *"},
		{`schema register subj --schema='{}'`, "schema.write subj"},
		{`connect pause conn`, "connect.state.write conn"},
		{`message produce orders --value=x`, "message.write orders"},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			ctx, mockAsset, _ := setupPolicyTest(t)
			asset := &asset_entity.Asset{
				ID: 1, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka,
				// 一条谁也匹配不上的 allowlist：让每条命令都落到 NeedConfirm，
				// 从而必然经过下面这个回调。
				CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{"nothing.matches *"}}),
			}
			mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

			var gotPolicyCommand string
			checker := permission.NewCommandPolicyChecker(
				func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
					if len(items) > 0 {
						gotPolicyCommand = items[0].Command
					}
					return permission.ApprovalResponse{Decision: "deny"}
				})
			ctx = permission.WithPolicyChecker(ctx, checker)

			if _, err := ExecKafkaOnAsset(ctx, asset, c.command, ""); err != nil {
				t.Fatalf("ExecKafkaOnAsset(%q) unexpected error: %v", c.command, err)
			}
			if gotPolicyCommand != c.wantPolicyCommand {
				t.Fatalf("ExecKafkaOnAsset(%q) reached the handler that checks %q, want the one that checks %q "+
					"— the family dispatch is wired to the wrong handler",
					c.command, gotPolicyCommand, c.wantPolicyCommand)
			}
		})
	}
}
