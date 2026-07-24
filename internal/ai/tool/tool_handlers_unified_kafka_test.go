package tool

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestHandleExec_KafkaChecksTwoTokenPolicyString 端到端锁住：exec 对 kafka 资产做权限
// 检查时，送进 CheckForAsset 的是双 token 策略串 "<action> <resource>"，不是模型写的
// 富命令串。
//
// 用审批回调观察——它是 CommandPolicyChecker 唯一的注入点，也正是策略串会被展示与
// 持久化（"allow all" 走 SaveGrantPattern）的地方。
//
// 命令选 `topic create` 是被两侧夹出来的，换成别的多半会让断言空转：内置默认策略
// （BuiltinKafkaMetadataReadOnly + BuiltinKafkaDangerousDeny，无自定义策略时生效）
// 的 allow 只覆盖读操作，所以读命令直接 Allow、回调不触发；而 Plan 原稿用的
// `topic delete` 命中 deny 列表的 "topic.delete *"，直接 Deny、回调同样不触发
// （实测 handleExec 返回 "Kafka operation denied by policy: topic.delete orders"）。
// `topic.create` 两边都不沾，才会落到 NeedConfirm 这条唯一能观测的路径。
// 回调答 deny，于是命令不会真的执行，测试不需要 kafka 集群。
//
// 两条断言分工不同，都不能删：第一条锁内容（必须是 canonicalize 的产物，不是原串，
// 且 flag 不参与策略匹配），第二条锁形状——policy.splitKafkaRule 用 strings.Fields
// 切，不是恰好 2 段时 MatchKafkaRule 对**任何**规则返回 false，deny 列表整个静默
// 失效（fail-open）。
func TestHandleExec_KafkaChecksTwoTokenPolicyString(t *testing.T) {
	m := setupUnified(t)

	asset := &asset_entity.Asset{ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka}
	m.EXPECT().FindByName(gomock.Any(), "kafka-prod").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()

	var gotCheckCommand string
	confirm := func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		if len(items) > 0 {
			gotCheckCommand = items[0].Command
		}
		return permission.ApprovalResponse{Decision: "deny"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeKafka)

	// 富命令串刻意写得与策略串差得远（family/verb 用空格分隔、带 target、多余空白、
	// 两个 flag），这样"没做规范化"与"做了规范化"的输出不可能碰巧相同。
	_, err := handleExec(ctx, map[string]any{
		"asset":   "kafka-prod",
		"command": `topic   create   orders --partitions=3 --replication-factor=2`,
	})
	if err != nil {
		t.Fatalf("handleExec unexpected error: %v", err)
	}

	if gotCheckCommand != "topic.create orders" {
		t.Fatalf("permission check saw %q, want the canonicalized policy string %q", gotCheckCommand, "topic.create orders")
	}
	if fields := len(strings.Fields(gotCheckCommand)); fields != 2 {
		t.Fatalf("permission check saw %d fields, want exactly 2 (splitKafkaRule requires it)", fields)
	}
}

// TestHandleExec_KafkaIsCheckedExactlyOnce 锁住：走 exec 的一条 kafka 命令**只**被
// 检查一次——handleExec 那一次（用 CanonicalizeKafkaCommand 的产物）。
//
// 曾经是两次：7 个 kafka_* 旧工具直连 HandleKafka*，而它们唯一的权限检查就是内部那次
// checkKafkaToolPermission；kafka 接入 exec 之后同一条命令被检查两次。用户可见的后果
// 有两个，都不是"多余但无害"：
//   - 选一次性「允许」（而非「全部允许」）会**弹两次审批对话框**——HandleConfirm
//     只在 allowAll 时持久化 grant（internal/ai/permission/checker.go），所以更安全的
//     那个选项反而被惩罚。
//   - 第一次允许、第二次拒绝时，checkKafkaToolPermission 返回 (result.Message, nil)，
//     handleExec 会把拒绝文案当作**成功的**工具结果返回给模型。
//
// 删掉内部那次检查必须与删除那 7 个工具在同一个 commit 里发生，否则中间会出现
// "已注册、模型可直接调用、完全无权限检查"的窗口（含 kafka_topic delete / kafka_acl
// delete）。两件事已在同一个 commit 完成，本测试是那件事的端到端验收证据：断言
// 回调**恰好**被触发一次。
//
// **逐 family 跑**，不是只跑一条命令：内部那次检查曾经在 7 个 HandleKafka* 里各有一份，
// 只覆盖 topic 的话，另外六个里任何一份被重新加回来都不会有测试失败——评审实测过，
// 把等价的检查加回 HandleKafkaSchema 之后全仓测试依然全绿。被这个断言接手的那六个
// TestKafka*PermissionStopsBeforeConnection 本来就是逐 family 写的。
func TestHandleExec_KafkaIsCheckedExactlyOnce(t *testing.T) {
	// 每个 family 一条命令，覆盖 kafkaFamilyHandlers 的全部 7 个分支。
	cases := []struct{ command, want string }{
		{`cluster overview`, "cluster.read *"},
		{`topic create orders --partitions=3 --replication-factor=2`, "topic.create orders"},
		{`consumer-group describe mygroup`, "consumer_group.read mygroup"},
		{`acl list`, "acl.read *"},
		{`schema register subj --schema='{}'`, "schema.write subj"},
		{`connect pause conn`, "connect.state.write conn"},
		{`message produce orders --value=x`, "message.write orders"},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			m := setupUnified(t)

			// 自定义 allowList 把默认策略的 allowList **整体替换**掉
			// （EffectiveKafkaPolicy：custom 非空时不与默认取并集），于是每条命令都落到
			// NeedConfirm、触发审批回调。不这么做的话 cluster/consumer-group 的只读命令
			// 会被内置 allowlist 直接放行、回调根本不触发，断言就成了空转——而空转恰恰
			// 是这个测试要防的东西。deny 侧仍与默认取并集，所以这不会放宽任何危险命令。
			asset := &asset_entity.Asset{
				ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka,
				CmdPolicy: `{"allow_list":["nothing.matches-any-command *"]}`,
			}
			m.EXPECT().FindByName(gomock.Any(), "kafka-prod").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
			m.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()

			var seen []string
			checker := permission.NewCommandPolicyChecker(
				func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
					for _, item := range items {
						seen = append(seen, item.Command)
					}
					return permission.ApprovalResponse{Decision: "allow"}
				})

			ctx := WithDocGate(context.Background(), NewDocGate())
			ctx = permission.WithPolicyChecker(ctx, checker)
			GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeKafka)

			// 批准之后命令真的会被执行，止于资产没有 Kafka 配置——正因为它执行到了执行器
			// 内部，"只检查了一次"才不是"根本没走到执行器"的假象。
			_, err := handleExec(ctx, map[string]any{
				"asset":   "kafka-prod",
				"command": tc.command,
			})
			if err == nil {
				t.Fatal("handleExec = nil error, want the executor to be reached and fail on the asset's empty Kafka config")
			}

			want := []string{tc.want}
			if !slices.Equal(seen, want) {
				t.Fatalf("approval saw %q, want %q — exactly one check, done by handleExec before dispatching to the executor", seen, want)
			}
		})
	}
}

// TestHandleExec_KafkaDenyListStopsBeforeConnecting 接手了六个
// TestKafka*PermissionStopsBeforeConnection（internal/ai/kafka_helper_test.go）留下的
// 不变式：deny 命中时，命令在**连上集群之前**就被拒绝。
//
// 那六个测试直接调 HandleKafka*，靠的是它们内部那次 checkKafkaToolPermission；
// 检查上移到 handleExec 之后，同一个不变式必须在新的、也是唯一的检查点上重新钉住，
// 否则它会随那六个测试一起消失。
//
// 判据是 err == nil：TestHandleExec_KafkaIsCheckedExactlyOnce 证明了一旦走进执行器，
// 就会因为资产没有 Kafka 配置报错。所以"无错误 + 返回拒绝文案"恰好等价于"没连过集群"。
//
// 用例表由默认生效的 deny 清单（即 BuiltinKafkaDangerousDeny，逐条对齐由文末的 guard
// 强制）派生，不是抄一张 family 清单：删掉 7 个 kafka_* 旧工具时，kafka_helper_test.go 里
// 逐 family 的 deny 用例随之消失，deny 路径一度收敛成单点覆盖。每条内置 deny 规则背后
// 是不同的 Kafka*Command 产出的策略串，任何一个的 (family, verb) → "<action> <resource>"
// 映射错位，都会让针对它的 deny 规则静默失配——本分支已经发生过五次的 fail-open 形状。
//
// 断言 slot.MatchedPattern 而不只看"被拒了"：deny 清单里同时有 7 条规则，只断言
// Decision==Deny 分不出"被我指的那条规则拒了"和"被另一条规则顺手拒了"，前者退化成
// 后者时测试照样绿。
func TestHandleExec_KafkaDenyListStopsBeforeConnecting(t *testing.T) {
	cases := []struct {
		name, command, wantRule, wantPolicy string
		// customDeny 标记"默认 deny 清单里没有这条规则，得由资产自定义策略装上"。
		// 文末的 guard 双向核对这个标记，它写错了会被抓到。
		customDeny bool
	}{
		{name: "topic delete", command: `topic delete orders`,
			wantRule: "topic.delete *", wantPolicy: "topic.delete orders"},
		{name: "topic delete-records", command: `topic delete-records orders --records='[{"partition":0,"offset":100}]'`,
			wantRule: "topic.records.delete *", wantPolicy: "topic.records.delete orders"},
		{name: "consumer-group delete", command: `consumer-group delete mygroup`,
			wantRule: "consumer_group.delete *", wantPolicy: "consumer_group.delete mygroup"},
		{name: "consumer-group reset-offset", command: `consumer-group reset-offset mygroup --topic=orders --mode=earliest`,
			wantRule: "consumer_group.offset.write *", wantPolicy: "consumer_group.offset.write mygroup"},
		{name: "acl delete", command: `acl delete --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow --host='*'`,
			wantRule: "acl.write *", wantPolicy: "acl.write *"},
		{name: "schema delete", command: `schema delete mysubject`,
			wantRule: "schema.delete *", wantPolicy: "schema.delete mysubject"},
		{name: "connect delete", command: `connect delete myconnector`,
			wantRule: "connect.delete *", wantPolicy: "connect.delete myconnector"},

		// 下面两个 family 的策略串在默认 deny 清单里一条都不沾，只能自带规则来测。
		// 不是补齐动作，是有意保留的覆盖：
		//   - message produce 产出 "message.write <topic>"，是真正的写操作，却没有任何
		//     内置 deny 规则（helper.TestKafkaBuiltinDeny_CoversEveryMutatingFamily 把这个
		//     缺口钉成已知状态）。用户自己写 message.write * 是唯一能拦住它的办法，
		//     那条路径必须有覆盖。
		//   - cluster 的 verb 全是只读，没有可 deny 的写操作；用只读 action 挂一条自定义
		//     deny，是让 KafkaClusterCommand 的策略串产出也走一遍 deny 路径的唯一方式。
		{name: "message produce (custom rule)", command: `message produce orders --value=hello`,
			wantRule: "message.write *", wantPolicy: "message.write orders", customDeny: true},
		{name: "cluster overview (custom rule)", command: `cluster overview`,
			wantRule: "cluster.read *", wantPolicy: "cluster.read *", customDeny: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := setupUnified(t)

			asset := &asset_entity.Asset{ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka}
			if c.customDeny {
				p, err := json.Marshal(asset_entity.KafkaPolicy{DenyList: []string{c.wantRule}})
				if err != nil {
					t.Fatalf("marshal policy: %v", err)
				}
				asset.CmdPolicy = string(p)
			}
			m.EXPECT().FindByName(gomock.Any(), "kafka-prod").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
			m.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()

			checker, called := newRecordingChecker()

			slot := &aictx.CheckResult{}
			ctx := aictx.WithCheckResultSlot(context.Background(), slot)
			ctx = WithDocGate(ctx, NewDocGate())
			ctx = permission.WithPolicyChecker(ctx, checker)
			GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeKafka)

			out, err := handleExec(ctx, map[string]any{"asset": "kafka-prod", "command": c.command})
			if err != nil {
				t.Fatalf("handleExec unexpected error: %v — a denied command must not reach the executor", err)
			}
			if !strings.Contains(out, c.wantPolicy) {
				t.Fatalf("handleExec = %q, want the denial to name the policy string %q", out, c.wantPolicy)
			}
			if slot.Decision != aictx.Deny || slot.DecisionSource != aictx.SourcePolicyDeny {
				t.Fatalf("audit decision = (%v, %q), want (%v, %q)",
					slot.Decision, slot.DecisionSource, aictx.Deny, aictx.SourcePolicyDeny)
			}
			if slot.MatchedPattern != c.wantRule {
				t.Fatalf("matched deny rule = %q, want %q — denied by a different rule than the one this case exercises",
					slot.MatchedPattern, c.wantRule)
			}
			if *called {
				t.Fatal("approval callback fired for a command the deny list already rejects")
			}
		})
	}

	// 表与内置清单双向对齐：少一条 → 新加的危险操作没有端到端覆盖；多一条（customDeny
	// 标错）→ 那条用例其实靠内置规则通过，自定义策略这条路径根本没被走到。
	covered := map[string]bool{}
	for _, c := range cases {
		if !c.customDeny {
			covered[c.wantRule] = true
		}
	}
	// 期望值取"资产没有自定义策略时实际生效"的 deny 清单（即 BuiltinKafkaDangerousDeny
	// 那 7 条），而不是在测试里抄一份：抄一份的话，往内置清单里加一条规则不会让任何
	// 测试变红，而"新加的危险操作没有端到端 deny 覆盖"正是这里要挡的洞。
	builtin := map[string]bool{}
	for _, rule := range policy.EffectiveKafkaPolicy(context.Background(), nil).DenyList {
		builtin[rule] = true
	}
	if !maps.Equal(covered, builtin) {
		t.Fatalf("cases cover deny rules %v, the default kafka deny list has %v — every builtin deny rule needs one end-to-end case",
			slices.Sorted(maps.Keys(covered)), slices.Sorted(maps.Keys(builtin)))
	}
	for _, c := range cases {
		if c.customDeny && builtin[c.wantRule] {
			t.Fatalf("case %q installs %q as a custom deny rule, but it is already in the default deny list — the custom-policy path it claims to cover is not exercised",
				c.name, c.wantRule)
		}
	}
}

// TestHandleExec_KafkaSyntaxErrorFailsBeforeApproval 锁住排序：kafka 命令的语法错误
// （未知 verb / 未知 flag / 缺必填 flag，全部由 CanonicalizeKafkaCommand 判定）必须在
// 审批回调触发之前返回，否则用户先被弹一次审批——选 "allow all" 还会落一条常驻
// grant——批准之后命令才因为解析失败而根本没跑。
func TestHandleExec_KafkaSyntaxErrorFailsBeforeApproval(t *testing.T) {
	cases := map[string]string{
		"unknown verb":         `topic nonsense orders`,
		"unknown flag":         `topic create orders --partitions=3 --replication-factor=2 --paritions=9`,
		"missing required":     `topic create orders --partitions=3`,
		"malformed flag value": `message browse orders --limit=1,000`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			m := setupUnified(t)

			asset := &asset_entity.Asset{ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka}
			m.EXPECT().FindByName(gomock.Any(), "kafka-prod").Return([]*asset_entity.Asset{asset}, nil).AnyTimes()
			m.EXPECT().Find(gomock.Any(), int64(7)).Return(asset, nil).AnyTimes()

			checker, called := newRecordingChecker()

			ctx := WithDocGate(context.Background(), NewDocGate())
			ctx = permission.WithPolicyChecker(ctx, checker)
			GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeKafka)

			_, err := handleExec(ctx, map[string]any{"asset": "kafka-prod", "command": command})
			if err == nil {
				t.Fatalf("handleExec(%q) = nil error, want rejection", command)
			}
			if *called {
				t.Fatalf("approval callback fired for %q, a command that cannot execute — canonicalize must run before the permission check", command)
			}
		})
	}
}
