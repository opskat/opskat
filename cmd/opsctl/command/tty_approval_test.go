package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"

	. "github.com/smartystreets/goconvey/convey"
)

// 终端提示的解析（spec Testing decisions：kind × 输入 → 决策）。
// 每种 ApprovalKind 只接受它该有的选项；"永久允许"（p）只出现在 single 上，
// 不出现在 once / batch / delete / extension 上；空输入判为拒绝；
// 非白名单输入返回 errTTYApprovalRetry 由调用方重新提示。
func TestParseTTYApprovalInput(t *testing.T) {
	Convey("parseTTYApprovalInput", t, func() {
		Convey("single 三选", func() {
			choice, err := parseTTYApprovalInput(permission.ApprovalKindSingle, "a")
			So(err, ShouldBeNil)
			So(choice, ShouldEqual, ttyAllowOnce)
			choice, err = parseTTYApprovalInput(permission.ApprovalKindSingle, "p")
			So(err, ShouldBeNil)
			So(choice, ShouldEqual, ttyAllowAlways)
			choice, err = parseTTYApprovalInput(permission.ApprovalKindSingle, "d")
			So(err, ShouldBeNil)
			So(choice, ShouldEqual, ttyDeny)
		})

		Convey("once/batch/delete/extension 两选，没有永久允许", func() {
			for _, kind := range []string{
				permission.ApprovalKindOnce,
				permission.ApprovalKindBatch,
				permission.ApprovalKindDelete,
				permission.ApprovalKindExtension,
			} {
				choice, err := parseTTYApprovalInput(kind, "a")
				So(err, ShouldBeNil)
				So(choice, ShouldEqual, ttyAllowOnce)
				choice, err = parseTTYApprovalInput(kind, "d")
				So(err, ShouldBeNil)
				So(choice, ShouldEqual, ttyDeny)

				_, err = parseTTYApprovalInput(kind, "p")
				So(errors.Is(err, errTTYApprovalRetry), ShouldBeTrue)
			}
		})

		Convey("空输入（直接回车）判为拒绝", func() {
			for _, kind := range []string{
				permission.ApprovalKindSingle,
				permission.ApprovalKindOnce,
				permission.ApprovalKindBatch,
			} {
				choice, err := parseTTYApprovalInput(kind, "")
				So(err, ShouldBeNil)
				So(choice, ShouldEqual, ttyDeny)
				choice, err = parseTTYApprovalInput(kind, "   ")
				So(err, ShouldBeNil)
				So(choice, ShouldEqual, ttyDeny)
			}
		})

		Convey("非白名单输入要求重新提示，不静默当成允许", func() {
			for _, input := range []string{"x", "yes", "A", "allow", "1"} {
				_, err := parseTTYApprovalInput(permission.ApprovalKindSingle, input)
				So(errors.Is(err, errTTYApprovalRetry), ShouldBeTrue)
			}
		})

		Convey("容忍行尾换行与首尾空白", func() {
			choice, err := parseTTYApprovalInput(permission.ApprovalKindSingle, "a\r\n")
			So(err, ShouldBeNil)
			So(choice, ShouldEqual, ttyAllowOnce)
		})
	})
}

// ttyApprovalKind 与桌面端 internal/app/opsctl.singleApprovalKind 同一条映射：
// 注册了权限检查的类型（exec/sql/redis/cp/k8s…）是 single；create/update 等
// 未注册类型是 once；delete / ext_tool 各自的 kind。
func TestTTYApprovalKind(t *testing.T) {
	Convey("ttyApprovalKind", t, func() {
		So(ttyApprovalKind("exec"), ShouldEqual, permission.ApprovalKindSingle)
		So(ttyApprovalKind("sql"), ShouldEqual, permission.ApprovalKindSingle)
		So(ttyApprovalKind("redis"), ShouldEqual, permission.ApprovalKindSingle)
		So(ttyApprovalKind("cp"), ShouldEqual, permission.ApprovalKindSingle)
		So(ttyApprovalKind("k8s"), ShouldEqual, permission.ApprovalKindSingle)
		So(ttyApprovalKind("create"), ShouldEqual, permission.ApprovalKindOnce)
		So(ttyApprovalKind("update"), ShouldEqual, permission.ApprovalKindOnce)
		So(ttyApprovalKind(permission.ApprovalTypeDelete), ShouldEqual, permission.ApprovalKindDelete)
		So(ttyApprovalKind("ext_tool"), ShouldEqual, permission.ApprovalKindExtension)
	})
}

func TestShellQuote(t *testing.T) {
	Convey("shellQuote 产出可直接粘贴的 POSIX 引用", t, func() {
		So(shellQuote("uptime"), ShouldEqual, `'uptime'`)
		So(shellQuote("it's here"), ShouldEqual, `'it'\''s here'`)
		So(shellQuote("rm -rf /; echo done"), ShouldEqual, `'rm -rf /; echo done'`)
	})
}

func ttyApprovalTestRequest() approval.ApprovalRequest {
	return approval.ApprovalRequest{
		Type:      "exec",
		AssetID:   3,
		AssetName: "web-1",
		Command:   "echo hi; echo bye",
		Detail:    "opsctl exec web-1 -- echo hi; echo bye",
		SessionID: "sess-tty",
	}
}

// runTTYApproval 的完整对话：提示写 out（stderr）、决策读 in（stdin）、
// 提示展示资产名/ID/类型、NormalizeGrantPatterns 归一化后的主体与审批项 detail；
// 决策值不含 allowAll（决策 13：永久允许走规则写入路径，不产生 grant）。
func TestRunTTYApproval(t *testing.T) {
	preserveTTYSeams(t)

	Convey("allow once：放行，审计来源 SourceUserAllow，MatchedPattern 记归一化主体", t, func() {
		var out bytes.Buffer
		res, err := runTTYApproval(context.Background(), ttyApprovalTestRequest(),
			strings.NewReader("a\n"), &out)
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		So(res.SessionID, ShouldEqual, "sess-tty")
		So(res.MatchedPattern, ShouldContainSubstring, "echo hi")
		So(res.MatchedPattern, ShouldContainSubstring, "echo bye")

		prompt := out.String()
		So(prompt, ShouldContainSubstring, "web-1")
		So(prompt, ShouldContainSubstring, "3")
		So(prompt, ShouldContainSubstring, "exec")
		So(prompt, ShouldContainSubstring, "opsctl exec web-1 -- echo hi; echo bye")
		// 复合命令必须按归一化后的子命令展示，人看到的才是真正要授出的范围
		So(strings.Count(prompt, "echo hi"), ShouldBeGreaterThan, 0)
		So(strings.Contains(prompt, "echo bye"), ShouldBeTrue)
	})

	Convey("deny / 空输入 / EOF 一律判为拒绝并给出 SourceUserDeny", t, func() {
		for _, in := range []string{"d\n", "\n", ""} {
			var out bytes.Buffer
			res, err := runTTYApproval(context.Background(), ttyApprovalTestRequest(),
				strings.NewReader(in), &out)
			So(err, ShouldNotBeNil)
			So(res.Decision, ShouldEqual, aictx.Deny)
			So(res.DecisionSource, ShouldEqual, aictx.SourceUserDeny)
		}
	})

	Convey("非白名单输入重新提示后接受合法输入", t, func() {
		var out bytes.Buffer
		res, err := runTTYApproval(context.Background(), ttyApprovalTestRequest(),
			strings.NewReader("x\ny\na\n"), &out)
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(strings.Count(out.String(), "Approval required"), ShouldBeGreaterThanOrEqualTo, 2)
	})

	Convey("永久允许：先写规则、写成功才放行（决策 13）", t, func() {
		var gotAssetID int64
		var gotType string
		var gotPatterns []string
		orig := writeAllowAlwaysRule
		writeAllowAlwaysRule = func(_ context.Context, assetID int64, approvalType string, patterns []string) error {
			gotAssetID, gotType, gotPatterns = assetID, approvalType, patterns
			return nil
		}
		defer func() { writeAllowAlwaysRule = orig }()

		var out bytes.Buffer
		res, err := runTTYApproval(context.Background(), ttyApprovalTestRequest(),
			strings.NewReader("p\n"), &out)
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		So(gotAssetID, ShouldEqual, 3)
		So(gotType, ShouldEqual, "exec")
		So(gotPatterns, ShouldResemble, []string{"echo hi", "echo bye"})
	})

	Convey("永久允许：规则写失败则不放行", t, func() {
		orig := writeAllowAlwaysRule
		writeAllowAlwaysRule = func(context.Context, int64, string, []string) error {
			return errors.New("rule write failed")
		}
		defer func() { writeAllowAlwaysRule = orig }()

		var out bytes.Buffer
		res, err := runTTYApproval(context.Background(), ttyApprovalTestRequest(),
			strings.NewReader("p\n"), &out)
		So(err, ShouldNotBeNil)
		So(res.Decision, ShouldEqual, aictx.Deny)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserDeny)
	})

	Convey("给人读的提示跟随策略语言，选项字母恒为 ASCII", t, func() {
		ctx := aictx.WithPolicyLang(context.Background(), "zh-cn")
		var out bytes.Buffer
		_, err := runTTYApproval(ctx, ttyApprovalTestRequest(), strings.NewReader("a\n"), &out)
		So(err, ShouldBeNil)
		So(out.String(), ShouldContainSubstring, "需要审批")
		So(out.String(), ShouldContainSubstring, "[a]")
		So(out.String(), ShouldContainSubstring, "[p]")
		So(out.String(), ShouldContainSubstring, "[d]")
	})

	Convey("once 类操作只给两选", t, func() {
		req := approval.ApprovalRequest{
			Type: "create", Detail: "opsctl create group --name prod", SessionID: "sess-tty",
		}
		var out bytes.Buffer
		prompt := func() string {
			out.Reset()
			_, _ = runTTYApproval(context.Background(), req, strings.NewReader("d\n"), &out)
			return out.String()
		}()
		So(prompt, ShouldContainSubstring, "[a]")
		So(prompt, ShouldContainSubstring, "[d]")
		So(prompt, ShouldNotContainSubstring, "[p]")
	})
}

// 多端点审批一次性列出全部条目、整批允许或整批拒绝（ApprovalKindBatch 语义）。
func TestRunTTYBatchApproval(t *testing.T) {
	preserveTTYSeams(t)
	items := []approval.BatchItem{
		{Type: "exec", AssetID: 3, AssetName: "web-1", Command: "uptime"},
		{Type: "sql", AssetID: 4, AssetName: "db-1", Command: "SELECT 1", Detail: "opsctl batch"},
	}

	Convey("整批允许", t, func() {
		var out bytes.Buffer
		res, err := runTTYBatchApproval(context.Background(), items,
			strings.NewReader("a\n"), &out)
		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		listing := out.String()
		So(listing, ShouldContainSubstring, "web-1")
		So(listing, ShouldContainSubstring, "uptime")
		So(listing, ShouldContainSubstring, "db-1")
		So(listing, ShouldContainSubstring, "SELECT 1")
		So(listing, ShouldNotContainSubstring, "[p]")
	})

	Convey("整批拒绝 / 空输入 / EOF", t, func() {
		for _, in := range []string{"d\n", "\n", ""} {
			var out bytes.Buffer
			res, err := runTTYBatchApproval(context.Background(), items,
				strings.NewReader(in), &out)
			So(err, ShouldNotBeNil)
			So(res.Decision, ShouldEqual, aictx.Deny)
			So(res.DecisionSource, ShouldEqual, aictx.SourceUserDeny)
		}
	})
}

// cp 的终端“永久允许”必须带上方向落规则：CheckType 携带 cp:read/cp:write，规则形态
// 是 “cp:read:<glob>”（permission 注册的方向化落点）。若把方向面交给按资产类型选
// 形状的 writeAllowAlwaysRuleImpl，路径会被写成一条 ssh 命令规则——本测试用前缀断言
// 挡住这条歧路。
func TestRunTTYApprovalCpDirectionAllowAlways(t *testing.T) {
	Convey("cp 的终端永久允许按 CheckType 方向落 cp:read:/cp:write: 规则", t, func() {
		env := newPolicyTestEnv(t)
		env.expectSSHAsset(t, 5, "web-01", nil)

		var out bytes.Buffer
		res, err := runTTYApproval(env.ctx, approval.ApprovalRequest{
			Type: "cp", CheckType: "cp:read", AssetID: 5, AssetName: "web-1",
			Command: "/etc/app/config.yml", SessionID: "sess-cp",
		}, strings.NewReader("p\n"), &out)

		So(err, ShouldBeNil)
		So(res.Decision, ShouldEqual, aictx.Allow)
		So(res.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		So(env.updates, ShouldHaveLength, 1)
		p, perr := env.updates[0].GetCommandPolicy()
		So(perr, ShouldBeNil)
		So(p.AllowList, ShouldContain, "cp:read:/etc/app/config.yml")
		So(p.AllowList, ShouldNotContain, "/etc/app/config.yml")
	})
}

// preserveTTYSeams 固定终端探测为非交互并隔离流注入缝，测试互不渗漏，
// 也保证交互式终端里跑 go test 时判据不被真实的 TTY 干扰。
func preserveTTYSeams(t *testing.T) {
	t.Helper()
	origStdin, origStderr := stdinIsTerminal, stderrIsTerminal
	origStreams := terminalApprovalStreams
	stdinIsTerminal = func() bool { return false }
	stderrIsTerminal = func() bool { return false }
	t.Cleanup(func() {
		stdinIsTerminal = origStdin
		stderrIsTerminal = origStderr
		terminalApprovalStreams = origStreams
	})
}
