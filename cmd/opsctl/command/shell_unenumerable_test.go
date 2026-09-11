package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"

	. "github.com/smartystreets/goconvey/convey"
)

// 拆不出子命令的 shell 命令（这里是未闭合的双引号）：独立 allow "*" 且没有 deny 时直接
// 放行；否则需要人确认，而无人可问时的结构化拒绝不能给出一条永远匹配不上的 policy allow，
// 只能说明解析失败的原因、引导修正命令。
const unparseableShell = `echo "`

func setAssetCommandPolicy(t *testing.T, env *opsctlExecTestEnv, name string, p asset_entity.CommandPolicy) {
	t.Helper()
	assets, err := asset_repo.Asset().List(env.ctx, asset_repo.ListOptions{})
	if err != nil {
		t.Fatalf("list test assets: %v", err)
	}
	for _, asset := range assets {
		if asset.Name == name {
			if err := asset.SetCommandPolicy(&p); err != nil {
				t.Fatalf("set command policy: %v", err)
			}
			return
		}
	}
	t.Fatalf("test asset %q not found", name)
}

// isolateApprovers 固定为非交互、桌面端不可达，返回是否尝试过连桌面端。
func isolateApprovers(t *testing.T) *bool {
	t.Helper()
	preserveTTYSeams(t)
	dialed := false
	origDial, origDataDir := dialApprovalSocket, sessionDataDir
	dialApprovalSocket = func(string) error {
		dialed = true
		return errors.New("connection refused")
	}
	sessionDataDir = func() string { return t.TempDir() }
	t.Cleanup(func() {
		dialApprovalSocket = origDial
		sessionDataDir = origDataDir
	})
	return &dialed
}

func TestCmdExec_SSHWildcardAllowsUnparseableCommand(t *testing.T) {
	restoreAssetRepoAfter(t)
	env := setupOpsctlExecAssets(t)
	dialed := isolateApprovers(t)
	setAssetCommandPolicy(t, env, "web-1", asset_entity.CommandPolicy{AllowList: []string{"*"}})

	var gotResult ApprovalResult
	var gotCommand string
	stub := execSSHStreamFn
	execSSHStreamFn = func(ctx context.Context, auditCtx context.Context, asset *asset_entity.Asset, command string, result ApprovalResult) int {
		gotResult, gotCommand = result, command
		return stub(ctx, auditCtx, asset, command, result)
	}
	t.Cleanup(func() { execSSHStreamFn = stub })

	code := cmdExec(env.ctx, env.handlers, []string{"web-1", "--type", "ssh", "--", unparseableShell}, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (policy allow, command sent to the remote shell)", code)
	}
	if env.sshStreamCalls != 1 || gotCommand != unparseableShell {
		t.Fatalf("ssh stream calls = %d, command = %q; want 1 call with the raw command", env.sshStreamCalls, gotCommand)
	}
	if *dialed {
		t.Fatal("policy allow must not reach the desktop approver")
	}
	// execSSHStreaming 用这份 ApprovalResult 写 audit_logs（result.ToCheckResult()）。
	if gotResult.DecisionSource != aictx.SourcePolicyAllow || gotResult.MatchedPattern != "*" {
		t.Fatalf("audit decision = %q / %q, want %q / %q",
			gotResult.DecisionSource, gotResult.MatchedPattern, aictx.SourcePolicyAllow, "*")
	}
}

func TestCmdExec_SSHUnparseableWithDenyRulesRefusesWithParseError(t *testing.T) {
	restoreAssetRepoAfter(t)
	env := setupOpsctlExecAssets(t)
	isolateApprovers(t)
	setAssetCommandPolicy(t, env, "web-1", asset_entity.CommandPolicy{
		AllowList: []string{"*"}, DenyList: []string{"reboot *"},
	})

	var code int
	stderr := captureStderr(t, func() {
		code = cmdExec(env.ctx, env.handlers, []string{"web-1", "--type", "ssh", "--", unparseableShell}, "")
	})

	if code != refusalExitCode {
		t.Fatalf("exit code = %d, want %d", code, refusalExitCode)
	}
	if env.sshStreamCalls != 0 {
		t.Fatal("refused command reached the remote shell")
	}
	firstLine, _, _ := strings.Cut(stderr, "\n")
	if firstLine != needsTTYMarker {
		t.Fatalf("first stderr line = %q, want %q", firstLine, needsTTYMarker)
	}
	if !strings.Contains(stderr, "reached EOF without closing quote") {
		t.Fatalf("stderr must carry the parse error, got:\n%s", stderr)
	}
	if strings.Contains(stderr, "policy allow") {
		t.Fatalf("stderr must not suggest a policy allow line that could never match, got:\n%s", stderr)
	}
}

func TestRequireApproval_ShellUnenumerable(t *testing.T) {
	Convey("非交互且桌面端不可达：拆不出子命令 → NEEDS TTY，带解析失败原因与原命令", t, func() {
		restoreAssetRepoAfter(t)
		env := setupOpsctlExecAssets(t)
		isolateApprovers(t)

		res, err := requireApproval(env.ctx, approval.ApprovalRequest{
			Type: "exec", AssetID: 2, AssetName: "web-1", Command: unparseableShell,
		})
		So(err, ShouldNotBeNil)
		var refusal *structuredRefusal
		So(errors.As(err, &refusal), ShouldBeTrue)
		So(refusal.marker, ShouldEqual, needsTTYMarker)
		So(err.Error(), ShouldContainSubstring, "reached EOF without closing quote")
		So(err.Error(), ShouldContainSubstring, "web-1")
		So(err.Error(), ShouldContainSubstring, unparseableShell)
		So(err.Error(), ShouldNotContainSubstring, "policy allow")
		So(res.Decision, ShouldEqual, aictx.Deny)
		So(res.MatchedPattern, ShouldBeEmpty)
	})

	Convey("批量结构化拒绝：拆不出子命令的条目不给 policy allow，说明原因", t, func() {
		_, err := refuseBatchApproval([]approval.BatchItem{
			{Type: "exec", AssetID: 3, AssetName: "web-1", Command: "uptime"},
			{Type: "exec", AssetID: 4, AssetName: "web-2", Command: unparseableShell},
		}, "sess-batch")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "opsctl policy allow 3 -- 'uptime'")
		So(err.Error(), ShouldNotContainSubstring, "opsctl policy allow 4")
		So(err.Error(), ShouldContainSubstring, "no rule can match")
	})

	Convey("终端审批：拆不出子命令的命令只有本次允许，没有永久允许", t, func() {
		var out bytes.Buffer
		res, err := runTTYApproval(context.Background(), approval.ApprovalRequest{
			Type: "exec", AssetID: 2, AssetName: "web-1", Command: unparseableShell,
		}, strings.NewReader("a\n"), &out)
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(out.String(), ShouldNotContainSubstring, "[p]")
	})
}
