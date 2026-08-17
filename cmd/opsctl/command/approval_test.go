package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/repository/asset_repo"

	. "github.com/smartystreets/goconvey/convey"
)

// restoreAssetRepoAfter 无条件恢复 asset_repo 的全局注册。既有 harness（exec/delete
// 的 setup*）只在原注册非 nil 时才还原，而测试进程的原始状态就是 nil——本文件按
// 字母序最先运行，注册的 mock 若漏给后面的测试（batch 用例的 DefaultAuditWriter 会
// 拿它解析资产名），会在别人家里以 unexpected call 收场。这里先取后还，nil 也照还。
func restoreAssetRepoAfter(t *testing.T) {
	t.Helper()
	pre := asset_repo.Asset()
	t.Cleanup(func() { asset_repo.RegisterAsset(pre) })
}

// 结构化拒绝契约（spec 决策 17）：有主体的操作（exec/cp/batch…）给 NEEDS AUTHORIZATION，
// 附资产名/ID/类型、被拒的归一化主体、当前生效 allow 规则提示（与旧 formatOfflineDenyMessage
// 同源）与可照抄的 opsctl policy allow 命令原文。
func TestFormatNeedsAuthorization(t *testing.T) {
	Convey("formatNeedsAuthorization", t, func() {
		entries := []refusalSubject{{
			assetName:    "web-1",
			assetID:      3,
			approvalType: "exec",
			patterns:     []string{"systemctl restart nginx"},
		}}
		body := formatNeedsAuthorization(context.Background(), entries,
			[]string{"ls *", "systemctl status *"})
		So(body, ShouldContainSubstring, "web-1")
		So(body, ShouldContainSubstring, "3")
		So(body, ShouldContainSubstring, "exec")
		So(body, ShouldContainSubstring, "systemctl restart nginx")
		So(body, ShouldContainSubstring, "ls *")
		So(body, ShouldContainSubstring, "systemctl status *")
		So(body, ShouldContainSubstring,
			"opsctl policy allow 3 -- 'systemctl restart nginx'")

		Convey("主体里的 shell 元字符照抄时被转义", func() {
			escaped := []refusalSubject{{
				assetName: "web-1", assetID: 3, approvalType: "exec",
				patterns: []string{"rm -rf /tmp/it's"},
			}}
			body := formatNeedsAuthorization(context.Background(), escaped, nil)
			So(body, ShouldContainSubstring, `'rm -rf /tmp/it'\''s'`)
		})

		Convey("多端点（batch）逐条给出 allow 命令", func() {
			entries := []refusalSubject{
				{assetName: "web-1", assetID: 3, approvalType: "exec", patterns: []string{"uptime"}},
				{assetName: "db-1", assetID: 4, approvalType: "sql", patterns: []string{"SELECT 1"}},
			}
			body := formatNeedsAuthorization(context.Background(), entries, nil)
			So(body, ShouldContainSubstring, "opsctl policy allow 3 -- 'uptime'")
			So(body, ShouldContainSubstring, "opsctl policy allow 4 -- 'SELECT 1'")
		})

		Convey("给人读的说明跟随策略语言", func() {
			ctx := aictx.WithPolicyLang(context.Background(), "zh-cn")
			body := formatNeedsAuthorization(ctx, entries[:1], nil)
			So(body, ShouldContainSubstring, "资产")
		})
	})
}

// 无主体的操作（create/update/delete）给 NEEDS TTY：说明任何规则都不能预授权、
// 给出人应自行执行的原命令原文、不建议重试、不给 policy allow 建议。
func TestFormatNeedsTTY(t *testing.T) {
	Convey("formatNeedsTTY", t, func() {
		req := approval.ApprovalRequest{Type: "delete", AssetID: 9, AssetName: "web-9"}
		body := formatNeedsTTY(context.Background(), req, "opsctl delete asset web-9")
		So(body, ShouldContainSubstring, "web-9")
		So(body, ShouldContainSubstring, "opsctl delete asset web-9")
		So(body, ShouldNotContainSubstring, "policy allow")
		So(body, ShouldContainSubstring, "do not retry")

		Convey("没有资产上下文时仍然给出原命令", func() {
			body := formatNeedsTTY(context.Background(),
				approval.ApprovalRequest{Type: "create"}, "opsctl create group --name prod")
			So(body, ShouldContainSubstring, "opsctl create group --name prod")
			So(body, ShouldNotContainSubstring, "policy allow")
		})
	})
}

func TestPolicyAllowCommand(t *testing.T) {
	Convey("policyAllowCommand 恒为英文 ASCII 且可直接粘贴（T5 落地语法：pattern 位置参数）", t, func() {
		So(policyAllowCommand(3, "exec", "systemctl restart nginx"), ShouldEqual,
			"opsctl policy allow 3 -- 'systemctl restart nginx'")
		Convey("cp 面按方向给 --type cp:read / cp:write", func() {
			So(policyAllowCommand(3, "cp:read", "/etc/*"), ShouldEqual,
				"opsctl policy allow 3 --type cp:read -- '/etc/*'")
			So(policyAllowCommand(4, "cp:write", "/var/x"), ShouldEqual,
				"opsctl policy allow 4 --type cp:write -- '/var/x'")
		})
	})
}

// 退出码契约：结构化拒绝 → 3，stderr 首行是裸标记（无 "Error: " 前缀）；
// 其他错误 → 1，保留普通前缀。
func TestWriteApprovalFailure(t *testing.T) {
	Convey("writeApprovalFailure", t, func() {
		refusal := &structuredRefusal{marker: needsAuthorizationMarker, body: "asset: web-1"}
		var out bytes.Buffer
		So(writeApprovalFailure(&out, refusal), ShouldEqual, refusalExitCode)
		So(refusalExitCode, ShouldEqual, 3)
		firstLine, _, _ := strings.Cut(out.String(), "\n")
		So(firstLine, ShouldEqual, "NEEDS AUTHORIZATION")

		out.Reset()
		So(writeApprovalFailure(&out, errors.New("boom")), ShouldEqual, 1)
		So(out.String(), ShouldEqual, "Error: boom\n")
	})
}

func TestApprovalResultToCheckResult(t *testing.T) {
	Convey("ApprovalResult.ToCheckResult", t, func() {
		ar := ApprovalResult{
			Decision:       aictx.Allow,
			DecisionSource: aictx.SourcePolicyAllow,
			MatchedPattern: "ls *",
			SessionID:      "sess-123",
		}
		cr := ar.ToCheckResult()
		So(cr.Decision, ShouldEqual, aictx.Allow)
		So(cr.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		So(cr.MatchedPattern, ShouldEqual, "ls *")
	})
}

// requireApproval 的审批人接线：三条路里可单测的两条（终端与结构化拒绝）。
// 桌面弹窗路径依赖真实 socket，由 chooseApprover 的注入测试覆盖选择逻辑本身。
func TestRequireApprovalRefusal(t *testing.T) {
	Convey("非交互且桌面端不可达：有主体 → NEEDS AUTHORIZATION，退出契约 3", t, func() {
		restoreAssetRepoAfter(t)
		env := setupOpsctlExecAssets(t) // 7 个资产 + Find/List mock，供 CheckPermission 用
		preserveTTYSeams(t)
		origDial := dialApprovalSocket
		origDataDir := sessionDataDir
		dialApprovalSocket = func(string) error { return errors.New("connection refused") }
		sessionDataDir = func() string { return t.TempDir() }
		t.Cleanup(func() {
			dialApprovalSocket = origDial
			sessionDataDir = origDataDir
		})

		res, err := requireApproval(env.ctx, approval.ApprovalRequest{
			Type: "exec", AssetID: 2, AssetName: "web-1", Command: "echo hi; echo bye",
		})
		So(err, ShouldNotBeNil)
		var refusal *structuredRefusal
		So(errors.As(err, &refusal), ShouldBeTrue)
		So(refusal.marker, ShouldEqual, "NEEDS AUTHORIZATION")
		firstLine, _, _ := strings.Cut(err.Error(), "\n")
		So(firstLine, ShouldEqual, "NEEDS AUTHORIZATION")
		So(err.Error(), ShouldContainSubstring, "opsctl policy allow 2 -- 'echo hi'")
		So(res.Decision, ShouldEqual, aictx.Deny)
		So(res.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)

		Convey("cp 主体按 CheckType 方向给出 --type cp:read 的照抄命令", func() {
			_, err := requireApproval(env.ctx, approval.ApprovalRequest{
				Type: "cp", CheckType: "cp:read", AssetID: 2, AssetName: "web-1",
				Command: "/etc/app/config.yml",
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "opsctl policy allow 2 --type cp:read -- '/etc/app/config.yml'")
		})

		Convey("无主体（create/update/delete）→ NEEDS TTY，附原命令、不给 policy allow", func() {
			ctx := withOriginCommand(env.ctx, "opsctl delete asset web-9")
			_, err := requireApproval(ctx, approval.ApprovalRequest{
				Type: "delete", AssetID: 9, AssetName: "web-9",
				Detail: "opsctl delete asset web-9",
			})
			So(err, ShouldNotBeNil)
			So(errors.As(err, &refusal), ShouldBeTrue)
			So(refusal.marker, ShouldEqual, "NEEDS TTY")
			So(err.Error(), ShouldContainSubstring, "opsctl delete asset web-9")
			So(err.Error(), ShouldNotContainSubstring, "policy allow")
		})
	})
}

func TestRequireApprovalTerminal(t *testing.T) {
	Convey("可交互：走终端提示，不拨 approval.sock", t, func() {
		restoreAssetRepoAfter(t)
		env := setupOpsctlExecAssets(t)
		origStdin, origStderr := stdinIsTerminal, stderrIsTerminal
		origStreams := terminalApprovalStreams
		stdinIsTerminal = func() bool { return true }
		stderrIsTerminal = func() bool { return true }
		dialed := false
		origDial := dialApprovalSocket
		dialApprovalSocket = func(string) error {
			dialed = true
			return nil
		}
		origDataDir := sessionDataDir
		sessionDataDir = func() string { return t.TempDir() }
		t.Cleanup(func() {
			stdinIsTerminal = origStdin
			stderrIsTerminal = origStderr
			terminalApprovalStreams = origStreams
			dialApprovalSocket = origDial
			sessionDataDir = origDataDir
		})

		var out bytes.Buffer
		terminalApprovalStreams = func() (io.Reader, io.Writer) {
			return strings.NewReader("a\n"), &out
		}

		res, err := requireApproval(env.ctx, approval.ApprovalRequest{
			Type: "exec", AssetID: 2, AssetName: "web-1", Command: "uptime", SessionID: "sess-t",
		})
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		So(dialed, ShouldBeFalse)
		So(out.String(), ShouldContainSubstring, "web-1")
	})
}

// requireBatchApproval 的两条可注入路径：终端整批审批与结构化拒绝。
func TestRequireBatchApprovalPaths(t *testing.T) {
	items := []approval.BatchItem{
		{Type: "exec", AssetID: 2, AssetName: "web-1", Command: "uptime"},
		{Type: "sql", AssetID: 1, AssetName: "cache-1", Command: "SELECT 1"},
	}

	Convey("非交互且桌面端不可达：NEEDS AUTHORIZATION，逐条给 allow 命令", t, func() {
		preserveTTYSeams(t)
		origDial := dialApprovalSocket
		origDataDir := sessionDataDir
		dialApprovalSocket = func(string) error { return errors.New("connection refused") }
		sessionDataDir = func() string { return t.TempDir() }
		t.Cleanup(func() {
			dialApprovalSocket = origDial
			sessionDataDir = origDataDir
		})

		res, err := requireBatchApproval(items, "")
		So(err, ShouldNotBeNil)
		var refusal *structuredRefusal
		So(errors.As(err, &refusal), ShouldBeTrue)
		So(refusal.marker, ShouldEqual, "NEEDS AUTHORIZATION")
		So(err.Error(), ShouldContainSubstring, "opsctl policy allow 2 -- 'uptime'")
		So(err.Error(), ShouldContainSubstring, "opsctl policy allow 1 -- 'SELECT 1'")
		So(res.Decision, ShouldEqual, aictx.Deny)
		So(res.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		So(res.SessionID, ShouldNotBeEmpty)
	})

	Convey("可交互：终端一次性列出全部条目、整批放行", t, func() {
		origStdin, origStderr := stdinIsTerminal, stderrIsTerminal
		origStreams := terminalApprovalStreams
		stdinIsTerminal = func() bool { return true }
		stderrIsTerminal = func() bool { return true }
		var out bytes.Buffer
		terminalApprovalStreams = func() (io.Reader, io.Writer) {
			return strings.NewReader("a\n"), &out
		}
		origDataDir := sessionDataDir
		sessionDataDir = func() string { return t.TempDir() }
		t.Cleanup(func() {
			stdinIsTerminal = origStdin
			stderrIsTerminal = origStderr
			terminalApprovalStreams = origStreams
			sessionDataDir = origDataDir
		})

		res, err := requireBatchApproval(items, "")
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		So(out.String(), ShouldContainSubstring, "web-1")
		So(out.String(), ShouldContainSubstring, "cache-1")
	})
}
