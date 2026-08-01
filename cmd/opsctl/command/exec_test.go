package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/serial_svc"

	"go.uber.org/mock/gomock"
)

// effectiveAuditCommand replicates DefaultAuditWriter.WriteToolCall's own resolution of
// the persisted `command` column (audit.go's `command := info.Command; if command == ""
// { command = ExtractCommandForAudit(...) }`), so tests can assert on what would actually
// land in audit_logs.command without standing up a real DB. mockAuditWriter records the
// raw ToolCallInfo verbatim — it does not replicate that fallback itself.
func effectiveAuditCommand(t *testing.T, info audit.ToolCallInfo) string {
	t.Helper()
	if info.Command != "" {
		return info.Command
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(info.ArgsJSON), &args); err != nil {
		t.Fatalf("unmarshal ArgsJSON: %v", err)
	}
	return audit.ExtractCommandForAudit(info.ToolName, args)
}

// captureStderr redirects os.Stderr for the duration of f and returns everything written
// to it. cmdExec (like the rest of this package's command handlers) reports errors via
// fmt.Fprintf(os.Stderr, ...) plus a bare exit code — this is the only way to assert on
// message content from outside, needed where the exit code alone can't distinguish two
// different failure causes (see TestCmdExec_KafkaDenyRuleEnforcedViaCanonicalCommand).
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	f()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stderr = orig
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(data)
}

// noSessionSerialManager is the minimal serial_svc.CommandManager fake that always
// reports "no active session" — mirrors internal/ai/tool/tool_handlers_unified_test.go's
// identical type (not reusable across packages: it's unexported there too).
type noSessionSerialManager struct{}

func (noSessionSerialManager) GetSessionByAssetID(_ int64) (serial_svc.CommandSession, bool) {
	return nil, false
}

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
	sshAuditSources  []string
}

func setupOpsctlExec(t *testing.T) *opsctlExecTestEnv {
	t.Helper()

	env := setupOpsctlExecAssets(t)

	origApproval := execApprovalFn
	execApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		env.approvalCalls++
		if env.approvalDecision == "allow" {
			return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-exec"}, nil
		}
		return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}, errors.New("denied")
	}
	t.Cleanup(func() { execApprovalFn = origApproval })

	return env
}

// setupOpsctlExecAssets builds the asset repo / ssh-stream / handler / audit stubs shared
// by every TestCmdExec_* test, but leaves execApprovalFn untouched. Most tests want it
// stubbed (setupOpsctlExec, above) so they never dial the real desktop approval socket;
// a few need the real requireApproval to run instead — e.g. asserting that a policy-level
// deny (I1: canonicalized before the check) short-circuits before ever reaching the
// desktop socket, which only the real function decides.
func setupOpsctlExecAssets(t *testing.T) *opsctlExecTestEnv {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	assets := []*asset_entity.Asset{
		{ID: 1, Name: "cache-1", Type: asset_entity.AssetTypeRedis},
		{ID: 2, Name: "web-1", Type: asset_entity.AssetTypeSSH},
		{ID: 3, Name: "desktop-1", Type: asset_entity.AssetTypeRDP},
		{ID: 4, Name: "serial-1", Type: asset_entity.AssetTypeSerial},
		{ID: 5, Name: "etcd-1", Type: asset_entity.AssetTypeEtcd},
		{ID: 6, Name: "kafka-1", Type: asset_entity.AssetTypeKafka},
		{ID: 7, Name: "oss-1", Type: asset_entity.AssetTypeOSS},
	}
	mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return(assets, nil).AnyTimes()
	// permission.CheckPermission (invoked from the real requireApproval, or from Step 3
	// of cmdBatch) resolves the asset again by numeric id via asset_svc.Asset().Get.
	for _, a := range assets {
		mockAsset.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
	}
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

	origStream := execSSHStreamFn
	execSSHStreamFn = func(_ context.Context, auditCtx context.Context, _ *asset_entity.Asset, _ string, _ ApprovalResult) int {
		env.sshStreamCalls++
		env.sshAuditSources = append(env.sshAuditSources, aictx.GetAuditSource(auditCtx))
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
	if len(env.sshAuditSources) != 1 || env.sshAuditSources[0] != "opsctl" {
		t.Errorf("ssh streaming audit source = %v, want [opsctl]", env.sshAuditSources)
	}
}

func TestCmdExec_DeniedApprovalAuditSourceIsOpsctl(t *testing.T) {
	env := setupOpsctlExec(t)

	code := cmdExec(env.ctx, env.handlers, []string{"web-1", "--", "systemctl restart review"}, "")
	if code == 0 {
		t.Fatal("denied approval must fail")
	}
	mock := opsctlAuditWriter.(*mockAuditWriter)
	if got := mock.lastSource(); got != "opsctl" {
		t.Fatalf("denied SSH approval audit source = %q, want opsctl", got)
	}
}

func TestCmdExec_SSHRemoteFailureAuditSourceIsOpsctl(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"
	execSSHStreamFn = func(_ context.Context, auditCtx context.Context, asset *asset_entity.Asset, command string, result ApprovalResult) int {
		writeOpsctlAudit(auditCtx, "exec", `{"asset_id":2,"command":"review"}`, "", errors.New("remote failure"), result.ToCheckResult())
		return 1
	}

	code := cmdExec(env.ctx, env.handlers, []string{"web-1", "--", "review"}, "")
	if code == 0 {
		t.Fatal("remote failure must fail")
	}
	mock := opsctlAuditWriter.(*mockAuditWriter)
	if got := mock.lastSource(); got != "opsctl" {
		t.Fatalf("SSH remote-failure audit source = %q, want opsctl", got)
	}
}

func TestCmdExec_SSHPolicyShortCircuitAuditSourceIsOpsctl(t *testing.T) {
	env := setupOpsctlExecAssets(t)
	assets, err := asset_repo.Asset().List(env.ctx, asset_repo.ListOptions{})
	if err != nil {
		t.Fatalf("list test assets: %v", err)
	}
	for _, asset := range assets {
		if asset.Name == "web-1" {
			if err := asset.SetCommandPolicy(&asset_entity.CommandPolicy{DenyList: []string{"rm *"}}); err != nil {
				t.Fatalf("set deny policy: %v", err)
			}
		}
	}

	code := cmdExec(env.ctx, env.handlers, []string{"web-1", "--", "rm -rf /"}, "")
	if code == 0 {
		t.Fatal("dangerous SSH command must be denied by policy")
	}
	if env.sshStreamCalls != 0 {
		t.Fatal("policy-denied SSH command reached streaming execution")
	}
	mock := opsctlAuditWriter.(*mockAuditWriter)
	if got := mock.lastSource(); got != "opsctl" {
		t.Fatalf("SSH policy-short-circuit audit source = %q, want opsctl", got)
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

// I2: an asset type with no executor at all (rdp is doc-only — put_asset/help work, exec
// does not) must fail before the user is ever shown a desktop approval dialog. Before this
// fix, opsctl only asserted --type before requireApproval; the executor lookup lived
// inside the handler, which requireApproval's approval popup gated.
func TestCmdExec_UnsupportedTypeFailsBeforeApproval(t *testing.T) {
	env := setupOpsctlExec(t) // desktop-1 是 rdp；approvalCalls 计数

	// Buffer stderr isn't captured here — the assertion is on exit code + approvalCalls,
	// matching the sibling ordering test (TestCmdExec_TypeAssertionFailsBeforeApproval).
	code := cmdExec(env.ctx, env.handlers, []string{"desktop-1", "--", "foo"}, "")

	if code == 0 {
		t.Fatal("an asset type with no executor must fail")
	}
	if env.approvalCalls != 0 {
		t.Errorf("approval ran %d times; the executor lookup must short-circuit first", env.approvalCalls)
	}
	if env.handlerCalls["exec"] != 0 {
		t.Errorf("the unified handler must never run for a type with no executor (%d calls)", env.handlerCalls["exec"])
	}
}

// I2: a serial asset with no active session must fail its precheck before requireApproval
// — otherwise the user approves a command that was always going to fail with "no active
// serial session" regardless of the answer.
func TestCmdExec_SerialPrecheckFailsBeforeApproval(t *testing.T) {
	env := setupOpsctlExec(t)
	env.ctx = helper.WithSerialManager(env.ctx, noSessionSerialManager{})

	code := cmdExec(env.ctx, env.handlers, []string{"serial-1", "--", "ls"}, "")

	if code == 0 {
		t.Fatal("a serial asset with no active session must fail")
	}
	if env.approvalCalls != 0 {
		t.Errorf("approval ran %d times; the precheck must short-circuit first", env.approvalCalls)
	}
	if env.handlerCalls["exec"] != 0 {
		t.Errorf("the unified handler must never run when the precheck fails (%d calls)", env.handlerCalls["exec"])
	}
}

// I2: a syntactically malformed etcd command (canonicalize's ParseCommand rejects it)
// must fail before requireApproval — the command was never going to execute regardless
// of what the user answers.
func TestCmdExec_EtcdMalformedCommandFailsBeforeApproval(t *testing.T) {
	env := setupOpsctlExec(t)

	code := cmdExec(env.ctx, env.handlers, []string{"etcd-1", "--", "put /only-key"}, "")

	if code == 0 {
		t.Fatal("a malformed etcd command must fail")
	}
	if env.approvalCalls != 0 {
		t.Errorf("approval ran %d times; canonicalize must short-circuit first", env.approvalCalls)
	}
	if env.handlerCalls["exec"] != 0 {
		t.Errorf("the unified handler must never run for a malformed command (%d calls)", env.handlerCalls["exec"])
	}
}

// I1 (the most important regression this task fixes): opsctl must canonicalize a command
// before matching it against policy, exactly like the AI exec tool does. Before this fix,
// requireApproval received the model/operator's raw richCommand ("topic delete orders" —
// 3 whitespace-separated tokens); the kafka policy engine's rules are keyed on the
// canonical "<action> <resource>" shape ("topic.delete orders" — exactly 2 tokens), so
// the built-in "topic.delete *" deny rule never matched the raw form and the command fell
// through to NeedConfirm — a deny silently downgraded to "ask the user".
//
// This test runs the *real* requireApproval (not the stubbed execApprovalFn other tests
// use): CheckPermission's Deny path returns before ever touching the desktop approval
// socket, so this is safe to run as a pure unit test — and it is precisely the code path
// that must see the canonical command.
func TestCmdExec_KafkaDenyRuleEnforcedViaCanonicalCommand(t *testing.T) {
	env := setupOpsctlExecAssets(t) // kafka-1；real requireApproval, not the stub

	var code int
	stderr := captureStderr(t, func() {
		code = cmdExec(env.ctx, env.handlers, []string{"kafka-1", "--", "topic delete orders"}, "")
	})

	if code == 0 {
		t.Fatal("a kafka topic-delete command must be denied by the built-in dangerous-op policy")
	}
	// "denied by policy" appears only on requireApproval's Stage 2 (CheckPermission)
	// short-circuit — the only path a *canonicalized* deny takes. If canonicalize were
	// skipped, "topic delete orders" (3 tokens) would fail to match the two-token deny
	// rule, fall through to NeedConfirm, and fail instead at Stage 3 (the desktop socket
	// is unavailable in this test) — a different, misleading failure that a bare
	// `code != 0` check cannot distinguish from a real policy deny.
	if !strings.Contains(stderr, "denied by policy") {
		t.Fatalf("expected a policy-deny error naming the reason, got stderr=%q", stderr)
	}
	if env.handlerCalls["exec"] != 0 {
		t.Errorf("the unified handler must never run for a denied command (%d calls)", env.handlerCalls["exec"])
	}
}

// Companion to the deny test above: message.write (produce) is NOT a built-in-dangerous
// kafka op, so with no asset-specific policy it must clear the policy check — proving the
// deny test above fails for the intended reason (a real, canonicalized policy match), not
// because every kafka command is unconditionally rejected by this test's plumbing.
func TestCmdExec_KafkaAllowedCommandClearsCanonicalPolicyCheck(t *testing.T) {
	env := setupOpsctlExecAssets(t)

	origApproval := execApprovalFn
	t.Cleanup(func() { execApprovalFn = origApproval })
	execApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
		// message.write needs user confirmation by default (NeedConfirm, not Allow) —
		// stub the desktop round trip so the test stays a unit test, but assert the
		// *canonical* command reached this point, proving Stage 2 of requireApproval
		// ran CheckPermission against "message.write orders", not the raw
		// "message produce orders".
		if req.Command != "message.write orders" {
			t.Errorf("approval request Command = %q, want the canonicalized form %q", req.Command, "message.write orders")
		}
		return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-kafka"}, nil
	}

	code := cmdExec(env.ctx, env.handlers, []string{"kafka-1", "--", "message produce orders"}, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if env.handlerCalls["exec"] != 1 {
		t.Errorf("unified exec handler ran %d times, want 1", env.handlerCalls["exec"])
	}
}

// G7 / S8 / A1 (e2e/scratch/2026-07-31-oss-ai-cli-operations/report.md §3.1): the approval
// dialog, the persisted grant pattern and the audit row must all show the same canonical
// DSL for a command opsctl itself would rewrite. The report reproduced a defect where the
// opsctl approval dialog for `object list verify-a` showed the canonicalized
// `object list verify-a/` (matching the AI entry's dialog *and* audit row) while the opsctl
// audit_logs.command column kept the raw `object list verify-a`.
//
// The unified (non-ssh) exec path runs through callHandler, which marshals the *raw*
// params (command must stay raw — it is what handleExec's own executor re-parses; feeding
// it the canonicalized display form would be lossy for k8s/kafka/mongo/etcd, see
// tool_handlers_unified.go's step 9 comment) and writes the audit row via writeOpsctlAudit,
// which never set audit.ToolCallInfo.Command — so DefaultAuditWriter always fell back to
// ExtractCommandForAudit, reading the raw command straight out of that params blob.
func TestCmdExec_OSSAuditCommandIsCanonical(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"

	code := cmdExec(env.ctx, env.handlers, []string{"oss-1", "--", "object list verify-a"}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	mock := opsctlAuditWriter.(*mockAuditWriter)
	got := effectiveAuditCommand(t, mock.lastCall())
	want := "object list verify-a/" // OSS canonicalizes a bare bucket target for "list" to end in "/"
	if got != want {
		t.Fatalf("persisted audit command = %q, want the canonical form %q shown by the approval dialog and used for the grant pattern", got, want)
	}
}

// Companion regression guard: ssh has no registered CanonicalizeFunc, so checkCommand ==
// command for it. The audited command must stay byte-identical to whatever the user typed
// — proving the fix above canonicalizes only when an asset type actually normalizes, not
// unconditionally (which would silently rewrite shell commands in the audit trail).
func TestCmdExec_SSHAuditCommandStaysRaw(t *testing.T) {
	env := setupOpsctlExec(t)
	env.approvalDecision = "allow"
	execSSHStreamFn = func(_ context.Context, auditCtx context.Context, asset *asset_entity.Asset, command string, result ApprovalResult) int {
		argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, asset.ID, command)
		writeOpsctlAudit(auditCtx, "exec", argsJSON, `{"exit_code":0}`, nil, result.ToCheckResult())
		return 0
	}

	command := "systemctl status nginx"
	code := cmdExec(env.ctx, env.handlers, []string{"web-1", "--", command}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	mock := opsctlAuditWriter.(*mockAuditWriter)
	got := effectiveAuditCommand(t, mock.lastCall())
	if got != command {
		t.Fatalf("ssh audit command = %q, want byte-identical raw command %q", got, command)
	}
}
