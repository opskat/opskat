package command

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"

	"go.uber.org/mock/gomock"
)

// opsctlDeleteTestEnv gives cmdDelete a fake asset repo (so "web-9" resolves), a fake
// deleteApprovalFn (so no real desktop socket is dialed), and fake delete_asset/
// delete_group handlers — counting calls to each so tests can assert "approval ran
// exactly once, dispatch happened exactly once, and only after approval succeeded."
type opsctlDeleteTestEnv struct {
	ctx context.Context

	handlers     map[string]tool.ToolHandlerFunc
	handlerCalls map[string]int

	// approvalDecision drives the stubbed deleteApprovalFn: "allow" or "deny".
	approvalDecision string
	approvalCalls    int
}

func setupOpsctlDelete(t *testing.T) *opsctlDeleteTestEnv {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 9, Name: "web-9", Type: asset_entity.AssetTypeSSH},
	}, nil).AnyTimes()
	// resolveAsset("web-9") does a name lookup (List above), but cmdDelete then
	// dispatches with the numeric id ("9") — the default audit writer
	// (internal/ai/audit/audit.go) resolves that numeric ref back to a name via
	// Find, independently of resolveAsset's own lookup.
	mockAsset.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&asset_entity.Asset{ID: 9, Name: "web-9", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})

	mockGroup := mock_group_repo.NewMockGroupRepo(ctrl)
	mockGroup.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&group_entity.Group{ID: 1, Name: "grp"}, nil).AnyTimes()
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(mockGroup)
	t.Cleanup(func() {
		if origGroup != nil {
			group_repo.RegisterGroup(origGroup)
		}
	})

	env := &opsctlDeleteTestEnv{
		ctx:              context.Background(),
		approvalDecision: "allow",
		handlerCalls:     make(map[string]int),
	}
	env.handlers = map[string]tool.ToolHandlerFunc{
		"delete_asset": func(context.Context, map[string]any) (string, error) {
			env.handlerCalls["delete_asset"]++
			return `{"id":9,"name":"web-9","message":"asset deleted"}`, nil
		},
		"delete_group": func(context.Context, map[string]any) (string, error) {
			env.handlerCalls["delete_group"]++
			return `{"id":1,"name":"grp","message":"group deleted"}`, nil
		},
	}

	origApproval := deleteApprovalFn
	deleteApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		env.approvalCalls++
		if env.approvalDecision == "deny" {
			return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}, errors.New("operation denied: user denied")
		}
		return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-delete"}, nil
	}
	t.Cleanup(func() { deleteApprovalFn = origApproval })

	return env
}

// opsctl help <asset> 不能被"打印 CLI 用法"的早期分支吃掉。
// root.go 在 bootstrap **之前**就拦截了 verb == "help"，那条分支既拿不到数据库
// 也拿不到 handler map——带参数时必须落到后面的 case。
func TestRootHelpVerb_WithArgIsNotTheCLIUsage(t *testing.T) {
	if isCLIUsageHelp([]string{"help"}) != true {
		t.Error(`bare "opsctl help" must still print CLI usage`)
	}
	if isCLIUsageHelp([]string{"help", "web-1"}) != false {
		t.Error(`"opsctl help web-1" must reach the asset help verb, not the CLI usage screen`)
	}
	for _, flag := range []string{"-h", "--help"} {
		if isCLIUsageHelp([]string{flag}) != true {
			t.Errorf("%s must still print CLI usage", flag)
		}
	}
}

// 删除必须拿到桌面端确认。CLI 侧的 checker 走 WithPreapproved 豁免，
// 所以确认由 requireApproval 承担——漏了它，opsctl 就成了绕过
// "delete 恒需确认" 的后门。
func TestCmdDelete_RequiresApproval(t *testing.T) {
	env := setupOpsctlDelete(t) // 资产 web-9；approvalCalls / handlerCalls 计数
	env.approvalDecision = "allow"

	code := cmdDelete(env.ctx, env.handlers, []string{"asset", "web-9"}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.approvalCalls != 1 {
		t.Errorf("approval ran %d times, want exactly 1", env.approvalCalls)
	}
	if env.handlerCalls["delete_asset"] != 1 {
		t.Errorf("delete_asset handler ran %d times, want 1", env.handlerCalls["delete_asset"])
	}
}

// 用户拒绝时不得派发。
func TestCmdDelete_DenyDoesNotDispatch(t *testing.T) {
	env := setupOpsctlDelete(t)
	env.approvalDecision = "deny"

	if code := cmdDelete(env.ctx, env.handlers, []string{"asset", "web-9"}, ""); code == 0 {
		t.Error("a denied delete must exit non-zero")
	}
	if env.handlerCalls["delete_asset"] != 0 {
		t.Errorf("denied delete must not dispatch, got %d calls", env.handlerCalls["delete_asset"])
	}
}

// delete group 走同一套 requireApproval 门禁,且 --delete-assets 原样透传给 handler。
func TestCmdDelete_Group(t *testing.T) {
	env := setupOpsctlDelete(t)

	t.Run("approved dispatches to delete_group", func(t *testing.T) {
		env.approvalCalls = 0
		env.handlerCalls = make(map[string]int)
		env.approvalDecision = "allow"

		code := cmdDelete(env.ctx, env.handlers, []string{"group", "1", "--delete-assets"}, "")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if env.approvalCalls != 1 {
			t.Errorf("approval ran %d times, want exactly 1", env.approvalCalls)
		}
		if env.handlerCalls["delete_group"] != 1 {
			t.Errorf("delete_group handler ran %d times, want 1", env.handlerCalls["delete_group"])
		}
	})

	t.Run("denied does not dispatch", func(t *testing.T) {
		env.approvalCalls = 0
		env.handlerCalls = make(map[string]int)
		env.approvalDecision = "deny"

		if code := cmdDelete(env.ctx, env.handlers, []string{"group", "1"}, ""); code == 0 {
			t.Error("a denied delete must exit non-zero")
		}
		if env.handlerCalls["delete_group"] != 0 {
			t.Errorf("denied delete must not dispatch, got %d calls", env.handlerCalls["delete_group"])
		}
	})
}

// 未知资源类型必须报错而不是静默派发。
func TestCmdDelete_UnknownResource(t *testing.T) {
	env := setupOpsctlDelete(t)
	if code := cmdDelete(env.ctx, env.handlers, []string{"widget", "1"}, ""); code == 0 {
		t.Error("unknown resource must exit non-zero")
	}
	if env.approvalCalls != 0 {
		t.Errorf("unknown resource must not reach approval, got %d calls", env.approvalCalls)
	}
}
