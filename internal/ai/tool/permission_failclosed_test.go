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

// registerFailClosedFixture 注册一个最小可执行类型，并让 42 号资产可解析。
// 返回的 *bool 记录执行体是否真的跑过——fail-closed 的关键断言不是"返回了错误"，
// 而是"命令没有执行"。
func registerFailClosedFixture(t *testing.T, assetType string) (context.Context, *bool) {
	t.Helper()

	executed := false
	permission.RegisterExecutor(assetType,
		func(_ context.Context, _ *asset_entity.Asset, _, _ string) (string, error) {
			executed = true
			return "ok", nil
		},
		"fake help doc for "+assetType,
		nil)
	t.Cleanup(func() { permission.UnregisterExecutorForTest(assetType) })

	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 42, Name: "fake-asset", Type: assetType}
	m.EXPECT().FindByName(gomock.Any(), "42").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(42)).Return(asset, nil).AnyTimes()

	ctx := WithDocGate(context.Background(), NewDocGate())
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), assetType)
	return ctx, &executed
}

// TestHandleExec_FailsClosedWithoutChecker 钉住 #249：context 里没有 PolicyChecker 时
// exec 必须失败，而不是跳过权限检查直接执行。
//
// 从前这里写的是 `if checker := GetPolicyChecker(ctx); checker != nil { 检查 }`——
// 语义是"注入缺失 == 放行"。它当前不可达（internal/app/ai/chat.go 里 policyChecker 与
// systemCfg 的赋值顺序传递性地保证了非 nil），但那个不变式既没有类型表达也没有测试
// 锁定：重排 activateProvider、或新增一条不经 systemCfg 守卫的入口，都会静默打开它。
// 这条测试就是那个缺失的锁。
func TestHandleExec_FailsClosedWithoutChecker(t *testing.T) {
	ctx, executed := registerFailClosedFixture(t, "test-fail-closed-exec")

	_, err := handleExec(ctx, map[string]any{"asset": "42", "command": "uptime"})
	if err == nil {
		t.Fatal("expected an error when no permission checker is wired, got nil")
	}
	if !strings.Contains(err.Error(), "permission checker not available") {
		t.Fatalf("got %q, want it to name the missing checker", err.Error())
	}
	if *executed {
		t.Fatal("command executed without any permission check")
	}
}

// TestHandleExec_PreapprovedRunsWithoutChecker 钉住 opsctl 那条路径没被 fail-closed 误伤：
// opsctl 在 requireApproval 里已经跑完策略 / Grant / 桌面审批（cmd/opsctl/command/approval.go），
// 派发 handler 时 context 里本来就没有 PolicyChecker（那是桌面 AI 会话专属的）。
// 它带着 permission.WithPreapproved 标记进来，exec 应当跳过第二次检查照常执行。
//
// 这条测试和上一条一起定义了"checker 为 nil"的两种含义：声明过 = 已检查，没声明 = 漏接线。
func TestHandleExec_PreapprovedRunsWithoutChecker(t *testing.T) {
	ctx, executed := registerFailClosedFixture(t, "test-fail-closed-preapproved")
	ctx = permission.WithPreapproved(ctx)

	out, err := handleExec(ctx, map[string]any{"asset": "42", "command": "uptime"})
	if err != nil {
		t.Fatalf("unexpected error on the preapproved path: %v", err)
	}
	if out != "ok" {
		t.Fatalf("got %q, want the executor's return value %q", out, "ok")
	}
	if !*executed {
		t.Fatal("preapproved call did not reach the executor")
	}
}

// TestHandleBatchCommand_FailsClosedWithoutChecker 钉住批量路径同样 fail-closed。
//
// batch_command 只对 AI 会话开放（不在 AllToolDefs 里，opsctl 走自己的 batch 子命令），
// 所以它不接受 WithPreapproved 豁免。从前 checker 为 nil 时每一项的 decision 都停在
// 初值 "allow"——整批命令一条不查地并发打到所有资产上，比单条 exec 的漏洞面大得多。
func TestHandleBatchCommand_FailsClosedWithoutChecker(t *testing.T) {
	setupUnified(t)

	_, err := handleBatchCommand(context.Background(), map[string]any{
		"commands": `[{"asset":"42","type":"exec","command":"uptime"}]`,
	})
	if err == nil {
		t.Fatal("expected an error when no permission checker is wired, got nil")
	}
	if !strings.Contains(err.Error(), "permission checker not available") {
		t.Fatalf("got %q, want it to name the missing checker", err.Error())
	}
}

// TestHandleBatchCommand_PreapprovedStillFailsClosed 钉住豁免的边界：batch 不吃
// WithPreapproved。opsctl 的 batch 子命令自己调 permission.CheckPermission，从不经过
// 这个 handler；哪天有人把 batch_command 加进 AllToolDefs，这条会红，逼他先想清楚
// 审批聚合（ConfirmFunc）在 CLI 下由谁承担。
func TestHandleBatchCommand_PreapprovedStillFailsClosed(t *testing.T) {
	setupUnified(t)

	_, err := handleBatchCommand(permission.WithPreapproved(context.Background()), map[string]any{
		"commands": `[{"asset":"42","type":"exec","command":"uptime"}]`,
	})
	if err == nil {
		t.Fatal("batch_command must not honour the opsctl preapproved exemption")
	}
}
