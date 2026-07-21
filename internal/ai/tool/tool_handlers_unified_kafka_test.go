package tool

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
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

// TestHandleExec_KafkaIsCheckedTwiceUntilLegacyToolsAreRemoved 如实记录注册后的中间态：
// 同一条命令会被检查**两次**——handleExec 一次（用 CanonicalizeKafkaCommand 的产物），
// HandleKafkaTopic 内部的 checkKafkaToolPermission 再一次（用它自己从结构化 args 拼出的
// 策略串）。7 个 kafka_* 工具还注册着、模型可以直接调用，摘掉内部那次检查必须与删除
// 那 7 个工具在同一个 commit 里发生，否则中间会出现"已注册、可直接调用、完全无权限
// 检查"的窗口。所以这里不掩盖重复，而是把它钉住。
//
// 顺带锁住一件让上面那步安全的事：两次检查看到的是**同一个**字符串。若两条路径对同一
// 条命令拼出不同的策略串，删掉内部那次就会静默改变授权语义。
//
// Task 8b 删掉内部检查后本测试会失败（次数变 1），这是有意的——届时把期望改成 1 并
// 把这段注释改写成"只检查一次"。
func TestHandleExec_KafkaIsCheckedTwiceUntilLegacyToolsAreRemoved(t *testing.T) {
	m := setupUnified(t)

	asset := &asset_entity.Asset{ID: 7, Name: "kafka-prod", Type: asset_entity.AssetTypeKafka}
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

	// 批准之后命令真的会被执行，止于资产没有 Kafka 配置——正因为它执行到了那里，
	// 才证明第二次检查确实发生在执行器内部，而不是被短路掉了。
	_, err := handleExec(ctx, map[string]any{
		"asset":   "kafka-prod",
		"command": `topic create orders --partitions=3 --replication-factor=2`,
	})
	if err == nil {
		t.Fatal("handleExec = nil error, want the executor to be reached and fail on the asset's empty Kafka config")
	}

	want := []string{"topic.create orders", "topic.create orders"}
	if len(seen) != len(want) || seen[0] != want[0] || seen[len(seen)-1] != want[len(want)-1] {
		t.Fatalf("approval saw %q, want %q (handleExec once + checkKafkaToolPermission once, same string)", seen, want)
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
