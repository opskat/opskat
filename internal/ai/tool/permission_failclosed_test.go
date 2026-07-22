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
// batch_exec 只对 AI 会话开放（不在 AllToolDefs 里，opsctl 走自己的 batch 子命令），
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
// 这个 handler；哪天有人把 batch_exec 加进 AllToolDefs，这条会红，逼他先想清楚
// 审批聚合（ConfirmFunc）在 CLI 下由谁承担。
func TestHandleBatchCommand_PreapprovedStillFailsClosed(t *testing.T) {
	setupUnified(t)

	_, err := handleBatchCommand(permission.WithPreapproved(context.Background()), map[string]any{
		"commands": `[{"asset":"42","type":"exec","command":"uptime"}]`,
	})
	if err == nil {
		t.Fatal("batch_exec must not honor the opsctl preapproved exemption")
	}
}

// TestHandleDelete_FailsClosedWithoutChecker 钉住 delete_asset/delete_group 在 context
// 里没有 PolicyChecker 时必须报错，且目标必须原封不动地留着——delete 从不查策略/grant，
// 唯一能拦住它的就是这个检查。没有它，permission.RequireChecker 之后的
// checker.ConfirmFunc() 会在 nil checker 上直接 panic（更糟：如果哪天这里被换成
// 不会 panic 的写法，那就是"注入缺失 == 放行"，delete 会在没有任何人点头的情况下执行）。
func TestHandleDelete_FailsClosedWithoutChecker(t *testing.T) {
	t.Run("delete_asset", func(t *testing.T) {
		env := setupCRUD(t)
		env.seedAsset("web-9", 0)

		_, err := handleDeleteAsset(context.Background(), map[string]any{"asset": "web-9"})
		if err == nil {
			t.Fatal("expected an error when no permission checker is wired, got nil")
		}
		if !strings.Contains(err.Error(), "permission checker not available") {
			t.Fatalf("got %q, want it to name the missing checker", err.Error())
		}
		if env.assetCount() != 1 {
			t.Error("asset must survive a fail-closed delete attempt")
		}
	})

	t.Run("delete_group", func(t *testing.T) {
		env := setupCRUD(t)
		g := env.seedGroup("prod")

		_, err := handleDeleteGroup(context.Background(), map[string]any{"id": float64(g.ID)})
		if err == nil {
			t.Fatal("expected an error when no permission checker is wired, got nil")
		}
		if !strings.Contains(err.Error(), "permission checker not available") {
			t.Fatalf("got %q, want it to name the missing checker", err.Error())
		}
		if env.groupCount() != 1 {
			t.Error("group must survive a fail-closed delete attempt")
		}
	})
}

// TestHandleDelete_FailsClosedWithConfirmFuncNil 钉住"有 checker 但确认回调为 nil"这一
// 分支：评审给出的具体回归场景是，如果有人为了"跟 exec/upload/download 保持一致"把
// delete 里的 permission.RequireChecker 换成 RequireCheckerOrPreapproved，带
// WithPreapproved 的 ctx 下 checker 会是 nil，直接调 checker.ConfirmFunc() 就会 panic；
// 下一步最自然的"修复"是加 `if checker != nil { …confirm… }`——那样删除就再也不弹框了。
// 这条测试钉住"确认回调缺失必须报错、不能悄悄放行"，逼着任何这类改动先想清楚。
func TestHandleDelete_FailsClosedWithConfirmFuncNil(t *testing.T) {
	t.Run("delete_asset", func(t *testing.T) {
		env := setupCRUD(t)
		env.seedAsset("web-9", 0)
		ctx := permission.WithPolicyChecker(context.Background(), permission.NewCommandPolicyChecker(nil))

		_, err := handleDeleteAsset(ctx, map[string]any{"asset": "web-9"})
		if err == nil {
			t.Fatal("expected an error when the checker has no confirm callback, got nil")
		}
		if env.assetCount() != 1 {
			t.Error("asset must survive when there is no confirm callback to ask the user")
		}
	})

	t.Run("delete_group", func(t *testing.T) {
		env := setupCRUD(t)
		g := env.seedGroup("prod")
		ctx := permission.WithPolicyChecker(context.Background(), permission.NewCommandPolicyChecker(nil))

		_, err := handleDeleteGroup(ctx, map[string]any{"id": float64(g.ID)})
		if err == nil {
			t.Fatal("expected an error when the checker has no confirm callback, got nil")
		}
		if env.groupCount() != 1 {
			t.Error("group must survive when there is no confirm callback to ask the user")
		}
	})
}

// TestHandleDelete_PreapprovedDoesNotExemptDelete 钉住 delete 不吃 opsctl 的
// WithPreapproved 豁免——它调用的是 permission.RequireChecker，不是
// RequireCheckerOrPreapproved（exec/batch_exec 用的那个）。这条与上面两条一起把
// "delete 的确认不可绕过"这条比 exec 更强的不变式钉在测试里，而不只是钉在注释里：
// 即使 ctx 显式标了 preapproved，没有 checker 也必须报错。
func TestHandleDelete_PreapprovedDoesNotExemptDelete(t *testing.T) {
	t.Run("delete_asset", func(t *testing.T) {
		env := setupCRUD(t)
		env.seedAsset("web-9", 0)
		ctx := permission.WithPreapproved(context.Background())

		_, err := handleDeleteAsset(ctx, map[string]any{"asset": "web-9"})
		if err == nil {
			t.Fatal("delete_asset must not honor the opsctl preapproved exemption")
		}
		if env.assetCount() != 1 {
			t.Error("asset must survive an unexempted, uncheckered delete attempt")
		}
	})

	t.Run("delete_group", func(t *testing.T) {
		env := setupCRUD(t)
		g := env.seedGroup("prod")
		ctx := permission.WithPreapproved(context.Background())

		_, err := handleDeleteGroup(ctx, map[string]any{"id": float64(g.ID)})
		if err == nil {
			t.Fatal("delete_group must not honor the opsctl preapproved exemption")
		}
		if env.groupCount() != 1 {
			t.Error("group must survive an unexempted, uncheckered delete attempt")
		}
	})
}
