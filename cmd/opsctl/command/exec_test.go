package command

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"

	"go.uber.org/mock/gomock"
)

// opsctlExecTestEnv bundles the stubs shared by TestCmdExec_*:
//   - assets: cache-1 (redis) / web-1 (ssh), resolved by name via resolveAsset's
//     List-based lookup (they aren't numeric IDs).
//   - approvalCalls/approvalDecision: stand in for execApprovalFn so tests never
//     dial the real desktop approval socket; approvalDecision == "allow" approves,
//     any other value (including the zero value) denies.
//   - handlerCalls: stands in for handlers["exec"] to assert whether the unified
//     handler ran and how many times.
//   - sshStreamCalls: stands in for execSSHStreamFn to assert whether the ssh
//     streaming path ran, without actually opening an SSH session.
type opsctlExecTestEnv struct {
	ctx              context.Context
	handlers         map[string]tool.ToolHandlerFunc
	approvalCalls    int
	approvalDecision string
	handlerCalls     map[string]int
	sshStreamCalls   int
}

func setupOpsctlExec(t *testing.T) *opsctlExecTestEnv {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	assets := []*asset_entity.Asset{
		{ID: 1, Name: "cache-1", Type: asset_entity.AssetTypeRedis},
		{ID: 2, Name: "web-1", Type: asset_entity.AssetTypeSSH},
	}
	mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return(assets, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})

	env := &opsctlExecTestEnv{
		ctx:          context.Background(),
		handlerCalls: map[string]int{},
	}

	origApproval := execApprovalFn
	execApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		env.approvalCalls++
		if env.approvalDecision == "allow" {
			return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-exec"}, nil
		}
		return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}, errors.New("denied")
	}
	t.Cleanup(func() { execApprovalFn = origApproval })

	origStream := execSSHStreamFn
	execSSHStreamFn = func(_ context.Context, _ context.Context, _ *asset_entity.Asset, _ string, _ ApprovalResult) int {
		env.sshStreamCalls++
		return 0
	}
	t.Cleanup(func() { execSSHStreamFn = origStream })

	env.handlers = map[string]tool.ToolHandlerFunc{
		"exec": func(_ context.Context, _ map[string]any) (string, error) {
			env.handlerCalls["exec"]++
			return `{"ok":true}`, nil
		},
	}

	mockAudit := &mockAuditWriter{}
	origWriter := opsctlAuditWriter
	opsctlAuditWriter = mockAudit
	t.Cleanup(func() { opsctlAuditWriter = origWriter })

	return env
}

// --type 与资产真实类型不符时必须在审批之前失败：不能让用户先批一条注定失败的命令。
func TestCmdExec_TypeAssertionFailsBeforeApproval(t *testing.T) {
	env := setupOpsctlExec(t) // 资产 cache-1 是 redis；approvalCalls 计数

	code := cmdExec(env.ctx, env.handlers, []string{"cache-1", "--type", "database", "--", "PING"}, "")

	if code == 0 {
		t.Fatal("a mismatched --type must fail")
	}
	if env.approvalCalls != 0 {
		t.Errorf("approval ran %d times; the assertion must short-circuit first", env.approvalCalls)
	}
}

// 非 ssh 资产改走统一 exec handler（此前只有 sql/redis/mongo 三个专用 verb 能碰它们）。
func TestCmdExec_NonSSHGoesThroughUnifiedHandler(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	code := cmdExec(env.ctx, env.handlers, []string{"cache-1", "--", "GET k"}, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.handlerCalls["exec"] != 1 {
		t.Errorf("unified exec handler ran %d times, want 1", env.handlerCalls["exec"])
	}
	if env.sshStreamCalls != 0 {
		t.Errorf("a redis asset must not go down the SSH streaming path (%d calls)", env.sshStreamCalls)
	}
}

// ssh 资产仍走流式路径：管道与 exit code 透传是已文档化的行为。
func TestCmdExec_SSHKeepsStreamingPath(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	code := cmdExec(env.ctx, env.handlers, []string{"web-1", "--", "uptime"}, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.sshStreamCalls != 1 {
		t.Errorf("ssh asset must keep the streaming path, got %d calls", env.sshStreamCalls)
	}
	if env.handlerCalls["exec"] != 0 {
		t.Errorf("ssh must not double-dispatch through the handler (%d calls)", env.handlerCalls["exec"])
	}
}

// --type 声明匹配资产真实类型时正常放行（反证：断言不是恒失败）。
func TestCmdExec_MatchingTypeAssertionPasses(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	code := cmdExec(env.ctx, env.handlers, []string{"cache-1", "--type", "redis", "--", "GET k"}, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.handlerCalls["exec"] != 1 {
		t.Errorf("unified exec handler ran %d times, want 1", env.handlerCalls["exec"])
	}
}

// approvalType 必须来自资产真实类型（ApprovalTypeFor），不能写死 "exec"：requireApproval
// 内部拿它去做策略/Grant 匹配，写死会让 redis/database/mongodb 资产的策略配置形同虚设
// （统统被当成 SSH 的 shell 命令策略检查）。
func TestCmdExec_ApprovalTypeMatchesAssetType(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	var seenType string
	execApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
		env.approvalCalls++
		seenType = req.Type
		return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-exec"}, nil
	}

	code := cmdExec(env.ctx, env.handlers, []string{"cache-1", "--", "GET k"}, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if seenType != "redis" {
		t.Errorf("approval Type = %q, want %q (asset's real type, for correct policy dispatch)", seenType, "redis")
	}
}
