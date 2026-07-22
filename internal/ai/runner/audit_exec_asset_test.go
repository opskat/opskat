package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	aitool "github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/service/serial_svc"
)

// setupExecAssetRepo registers a mock AssetRepo for the duration of the test,
// mirroring the pattern used across internal/ai/{assetref,tool}'s tests
// (mock_asset_repo.RegisterAsset + t.Cleanup restore).
func setupExecAssetRepo(t *testing.T) *mock_asset_repo.MockAssetRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	// Restore unconditionally, including back to nil: no other test in this package
	// registers an AssetRepo of its own before relying on args["asset_id"]-only
	// attribution, so an `if orig != nil` guard here would silently leave this
	// exhausted mock (and its now-completed *testing.T) as the process-wide default,
	// and a later test's async audit write would panic trying to call it.
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	return m
}

func registerMockAuditRepo(t *testing.T) *mockAuditRepo {
	t.Helper()
	mockRepo := &mockAuditRepo{}
	origRepo := audit_repo.Audit()
	audit_repo.RegisterAudit(mockRepo)
	t.Cleanup(func() { audit_repo.RegisterAudit(origRepo) })
	return mockRepo
}

func okResult() (*agent.ToolResultBlock, error) {
	return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}}, nil
}

// waitForAuditEntry waits for and returns the audit entry matching toolName and
// assetID, reading mockRepo.logs under its mutex throughout (unlike a bare
// mockRepo.logs[0], which races Create()'s concurrent append).
//
// auditMiddleware writes via a fire-and-forget goroutine (`go
// auditWriter.WriteToolCall(...)`) into audit_repo's process-global registry. A
// goroutine spawned by an earlier test can still be in flight when this test
// registers its own mock, and land in it after this test's own write completes --
// producing a stray entry (observed in the wild as a bogus `"subagent-":""` row).
// waitForAudit(want:1) + logs[0] would then grab that stray row instead of this
// test's own. Selecting by (ToolName, AssetID) picks this test's own entry
// regardless of what else lands in the shared slice. This does not fix the
// underlying global-registry/fire-and-forget flake -- it only makes each test
// immune to picking up a neighbor's entry.
func waitForAuditEntry(t *testing.T, m *mockAuditRepo, toolName string, assetID int64) *audit_entity.AuditLog {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for _, e := range m.logs {
			if e.ToolName == toolName && e.AssetID == assetID {
				m.mu.Unlock()
				return e
			}
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("audit entry for tool=%q assetID=%d not found within 2s", toolName, assetID)
	return nil
}

// TestAuditMiddleware_ExecToolResolvesAssetByID and ...ByName lock in the fix for
// exec/help's asset attribution: their asset identifier is args["asset"] (numeric id
// or name string), not args["asset_id"]/args["id"], so it needs assetref.Resolve, not
// aictx.ArgInt64.
func TestAuditMiddleware_ExecToolResolvesAssetByID(t *testing.T) {
	Convey("exec 工具用数字 id 作为 asset 参数时，审计能解析出资产归属", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(7)).Return(
			&asset_entity.Asset{ID: 7, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil)

		runAuditChain(t, context.Background(), "exec", "tu_asset_id",
			map[string]any{"asset": "7", "command": "uptime"}, nil, okResult)

		entry := waitForAuditEntry(t, mockRepo, "exec", 7)
		So(entry.AssetID, ShouldEqual, int64(7))
		So(entry.AssetName, ShouldEqual, "web-1")
		So(entry.Command, ShouldEqual, "uptime")
	})
}

func TestAuditMiddleware_ExecToolResolvesAssetByName(t *testing.T) {
	Convey("exec 工具用名称字符串作为 asset 参数时，审计能解析出资产归属", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "web-1").Return([]*asset_entity.Asset{
			{ID: 8, Name: "web-1", Type: asset_entity.AssetTypeSSH},
		}, nil)

		runAuditChain(t, context.Background(), "exec", "tu_asset_name",
			map[string]any{"asset": "web-1", "command": "uptime"}, nil, okResult)

		entry := waitForAuditEntry(t, mockRepo, "exec", 8)
		So(entry.AssetID, ShouldEqual, int64(8))
		So(entry.AssetName, ShouldEqual, "web-1")
	})
}

func TestAuditMiddleware_HelpToolResolvesAssetAttribution(t *testing.T) {
	Convey("help 工具同样能解析出资产归属", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "9").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(9)).Return(
			&asset_entity.Asset{ID: 9, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

		runAuditChain(t, context.Background(), "help", "tu_help",
			map[string]any{"asset": "9"}, nil, func() (*agent.ToolResultBlock, error) {
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "docs"}}}, nil
			})

		entry := waitForAuditEntry(t, mockRepo, "help", 9)
		So(entry.AssetID, ShouldEqual, int64(9))
		So(entry.AssetName, ShouldEqual, "cache-1")
	})
}

// staticAssetRepo resolves a single fixed asset for both id- and name-based
// lookups, standing in for assetref.Resolve's FindByName-then-Find fallback.
// TestAuditMiddleware_ExecToolCanonicalizesK8sCommand drives the real "exec" tool
// (aitool.Tools()) through the real auditMiddleware AND a real
// permission.CommandPolicyChecker, which between them resolve/look up the asset
// several times over (assetref.Resolve in the middleware, assetref.Resolve again
// in handleExec, asset_svc.Asset().Get in the policy checker's group/grant/confirm
// steps) -- a strict gomock.EXPECT() call-count per lookup would be brittle and
// say nothing the test cares about. A plain stub sidesteps that.
type staticAssetRepo struct {
	asset_repo.AssetRepo
	asset *asset_entity.Asset
}

func (r *staticAssetRepo) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	if id == r.asset.ID {
		return r.asset, nil
	}
	return nil, errors.New("asset not found")
}

func (r *staticAssetRepo) FindByName(_ context.Context, name string) ([]*asset_entity.Asset, error) {
	if name == r.asset.Name {
		return []*asset_entity.Asset{r.asset}, nil
	}
	return nil, nil
}

// findExecTool locates a tool by name among aitool.Tools() (the same registry
// production wires into the real agent), so tests can drive the actual handler
// instead of a stub.
func findExecTool(t *testing.T, name string) agent.Tool {
	t.Helper()
	for _, tl := range aitool.Tools() {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found in aitool.Tools()", name)
	return nil
}

// TestAuditMiddleware_ExecToolCanonicalizesK8sCommand is the regression lock for
// defect B: exec's own extractor in extractor_default.go records args["command"]
// verbatim, which is correct for ssh/serial/redis/database (raw == effective there)
// but wrong for k8s, where handleExec canonicalizes the command (injects
// --context/--namespace) before the permission check / approval dialog sees it.
// Approved-but-not-what's-audited is the exact bug class this locks against.
//
// It drives the real "exec" tool (aitool.Tools(), not a stub) through a real
// *permission.CommandPolicyChecker so the test observes both sides of the
// invariant it protects, instead of asserting a hardcoded literal computed only
// from resolveAssetForAudit in isolation: "captured" is the command
// handleExec's own canonicalization handed to CheckForAsset (what the real
// approval dialog would show, via the confirm callback's ApprovalItem.Command);
// entry.Command is what auditMiddleware's independent canonicalization recorded.
// A literal-only assertion would keep passing if handleExec's canonicalization
// inputs changed and resolveAssetForAudit's copy silently fell out of step with
// it; asserting the two live values against each other (not just against the
// same string) is what actually catches that divergence.
func TestAuditMiddleware_ExecToolCanonicalizesK8sCommand(t *testing.T) {
	Convey("k8s 资产的 exec 审计记录规范化后的命令，与审批弹窗看到的一致", t, func() {
		mockRepo := registerMockAuditRepo(t)

		asset := &asset_entity.Asset{ID: 42, Name: "k8s-1", Type: asset_entity.AssetTypeK8s}
		if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
			Kubeconfig: "enc-kubeconfig",
			Context:    "prod-ctx",
			Namespace:  "prod-ns",
		}); err != nil {
			t.Fatalf("set k8s config: %v", err)
		}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(&staticAssetRepo{asset: asset})
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		// Fake confirm callback: captures the command the real policy checker's
		// CheckForAsset handed down to confirmation -- i.e. what the approval
		// dialog would render -- and denies, so the (unrelated to this test) real
		// k8s executor never actually runs.
		var captured string
		checker := permission.NewCommandPolicyChecker(
			func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
				if len(items) > 0 {
					captured = items[0].Command
				}
				return permission.ApprovalResponse{Decision: "deny"}
			})
		ctx := permission.WithPolicyChecker(context.Background(), checker)

		execTool := findExecTool(t, "exec")
		td := &agent.ToolDispatcher{
			Tools:      []agent.Tool{execTool},
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
		}
		td.Run(ctx, agent.DispatchInput{
			ToolName:  "exec",
			ToolUseID: "tu_k8s_sync",
			Input:     map[string]any{"asset": "42", "command": "apply -f deploy.yaml"},
		})

		entry := waitForAuditEntry(t, mockRepo, "exec", 42)
		want := "kubectl --context prod-ctx --namespace prod-ns apply -f deploy.yaml"
		So(captured, ShouldEqual, want)
		So(entry.Command, ShouldEqual, want)
		So(entry.Command, ShouldEqual, captured)
	})
}

// TestAuditMiddleware_ExtExecToolDoesNotCanonicalizeAsAssetCommand is the regression
// lock for M4: resolveAssetForAudit used to trigger its canonicalize step off argument
// *shape* alone (an "asset" string + a "command" string), not the tool name. Task 9
// renamed exec_tool to ext_exec(asset, command) — the same shape "exec" uses — so this
// path started firing for extension calls too. ext_exec's command is an extension's own
// invocation syntax (`<extension> <tool> --flag=value`), never the target asset type's
// exec DSL; running a k8s asset's CanonicalizeFunc on it (BuildK8sCommandPlan) doesn't
// error (cmdline.Words happily tokenizes "myext mytool --flag=x" as if it were kubectl
// args) — it silently rewrites the audit row into a `kubectl ... --context=... ` string
// that was never approved and never executed, exactly the divergence
// TestAuditMiddleware_ExecToolCanonicalizesK8sCommand above exists to prevent for the
// tool that *is* supposed to canonicalize.
//
// The fix gates the canonicalize step by an audit.RegisterCanonicalizingTool allow-list
// (only "exec" registers, extractor_default.go) rather than branching on tool name in
// resolveAssetForAudit itself — same registration pattern as RegisterExtractor /
// RegisterGroupScopedTool in package audit.
func TestAuditMiddleware_ExtExecToolDoesNotCanonicalizeAsAssetCommand(t *testing.T) {
	Convey("ext_exec 审计记录原始扩展命令，不按资产类型的 exec DSL 规范化改写", t, func() {
		mockRepo := registerMockAuditRepo(t)

		asset := &asset_entity.Asset{ID: 43, Name: "k8s-2", Type: asset_entity.AssetTypeK8s}
		if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
			Kubeconfig: "enc-kubeconfig",
			Context:    "prod-ctx",
			Namespace:  "prod-ns",
		}); err != nil {
			t.Fatalf("set k8s config: %v", err)
		}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(&staticAssetRepo{asset: asset})
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		extExecTool := findExecTool(t, "ext_exec")
		td := &agent.ToolDispatcher{
			Tools:      []agent.Tool{extExecTool},
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
		}
		const rawCommand = "myext mytool --flag=x"
		td.Run(context.Background(), agent.DispatchInput{
			ToolName:  "ext_exec",
			ToolUseID: "tu_ext_exec_k8s",
			Input:     map[string]any{"asset": "43", "command": rawCommand},
		})

		entry := waitForAuditEntry(t, mockRepo, "ext_exec", 43)
		// Asset attribution is still correct and desired -- only the command rewrite is the bug.
		So(entry.AssetID, ShouldEqual, int64(43))
		So(entry.AssetName, ShouldEqual, "k8s-2")
		So(entry.Command, ShouldEqual, rawCommand)
		So(entry.Command, ShouldNotContainSubstring, "kubectl")
	})
}

// phaseGatedAssetRepo resolves successfully only until *toolStarted flips true, then
// fails -- standing in for an asset that becomes unresolvable (e.g. soft-deleted,
// status != Active, see asset_repo.Find/FindByName's "AND status = Active" filter)
// partway through a tool call.
type phaseGatedAssetRepo struct {
	asset_repo.AssetRepo
	asset       *asset_entity.Asset
	toolStarted *bool
}

func (r *phaseGatedAssetRepo) Find(_ context.Context, _ int64) (*asset_entity.Asset, error) {
	if *r.toolStarted {
		return nil, errors.New("record not found")
	}
	return r.asset, nil
}

func (r *phaseGatedAssetRepo) FindByName(_ context.Context, _ string) ([]*asset_entity.Asset, error) {
	return nil, nil
}

// TestAuditMiddleware_ExecToolResolvesAssetBeforeToolRuns is the regression lock for
// the ordering sub-requirement: resolution must happen before the tool runs, not
// after. If it happened after (e.g. audit re-resolving post-hoc via asset_repo.Find),
// a future delete_asset tool would flip the asset's status during its own call and
// the post-hoc lookup would come back empty -- losing the name for exactly the
// operation where it matters most. This test simulates that status flip with a repo
// that only resolves successfully until the tool itself starts running.
func TestAuditMiddleware_ExecToolResolvesAssetBeforeToolRuns(t *testing.T) {
	Convey("资产解析必须发生在工具执行之前，而不是之后", t, func() {
		mockRepo := registerMockAuditRepo(t)

		asset := &asset_entity.Asset{ID: 5, Name: "web-5", Type: asset_entity.AssetTypeSSH}
		toolStarted := false
		repo := &phaseGatedAssetRepo{asset: asset, toolStarted: &toolStarted}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(repo)
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		runAuditChain(t, context.Background(), "exec", "tu_ordering",
			map[string]any{"asset": "5", "command": "uptime"}, nil,
			func() (*agent.ToolResultBlock, error) {
				toolStarted = true
				return okResult()
			})

		entry := waitForAuditEntry(t, mockRepo, "exec", 5)
		So(entry.AssetID, ShouldEqual, int64(5))
		So(entry.AssetName, ShouldEqual, "web-5")
	})
}

// TestAuditMiddleware_ExecGateBlockedIsDistinguishable is the regression lock for the
// finding that a gate-blocked exec wrote an audit row indistinguishable from a
// successful execution.
//
// auditMiddleware resolves and canonicalizes before c.Next() (by design -- see
// resolveAssetForAudit), so when handleExec short-circuits at the doc gate the row
// still lands with the asset attributed and args["command"] fully populated. The gate
// returns guidance text rather than a Go error (deliberately: the model self-corrects
// mid-turn), so success stays 1 too. Before the fix nothing on that row said the
// command never ran -- a policy-denied row at least carries a populated decision,
// this one carried none, and `WHERE success = 1` counted it as an executed command.
//
// The fix reuses the existing RecordDecision plumbing rather than adding a column:
// decision=deny + decision_source=exec_gate_blocked. Asserting DecisionSource (not
// just a non-empty Decision) is what distinguishes it from a genuine policy denial.
func TestAuditMiddleware_ExecGateBlockedIsDistinguishable(t *testing.T) {
	Convey("门禁短路的 exec 审计行必须能跟执行成功的行区分开", t, func() {
		mockRepo := registerMockAuditRepo(t)

		asset := &asset_entity.Asset{ID: 77, Name: "cache-77", Type: asset_entity.AssetTypeRedis}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(&staticAssetRepo{asset: asset})
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		// A real, empty DocGate: redis was never marked documented in this conversation,
		// so handleExec short-circuits at the gate.
		ctx := aitool.WithDocGate(context.Background(), aitool.NewDocGate())

		execTool := findExecTool(t, "exec")
		td := &agent.ToolDispatcher{
			Tools:      []agent.Tool{execTool},
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
		}
		td.Run(ctx, agent.DispatchInput{
			ToolName:  "exec",
			ToolUseID: "tu_gate_blocked",
			Input:     map[string]any{"asset": "77", "command": "SET foo bar"},
		})

		entry := waitForAuditEntry(t, mockRepo, "exec", 77)
		// The row still looks like a successful call on every pre-existing signal --
		// which is precisely why the decision columns have to carry the distinction.
		So(entry.Success, ShouldEqual, 1)
		So(entry.Command, ShouldEqual, "SET foo bar")
		So(entry.Decision, ShouldEqual, "deny")
		So(entry.DecisionSource, ShouldEqual, aictx.SourceExecGateBlocked)
	})
}

// TestAuditMiddleware_ExecUnsupportedTypeIsDistinguishable covers the sibling
// short-circuit: an asset type with no registered executor. This one does return a Go
// error (success=0), so it was already distinguishable from a successful run -- but
// not attributable to a reason, which is what DecisionSource adds. Locking it here
// keeps the three short-circuit paths consistent.
func TestAuditMiddleware_ExecUnsupportedTypeIsDistinguishable(t *testing.T) {
	Convey("未注册执行器的类型，审计行要标出短路原因", t, func() {
		mockRepo := registerMockAuditRepo(t)

		// vnc: a remote-desktop type that has no command execution at all, so it is a
		// stable stand-in for "no executor registered". kafka used to play this role and
		// stopped being unsupported the moment it joined the unified exec.
		asset := &asset_entity.Asset{ID: 78, Name: "vnc-78", Type: asset_entity.AssetTypeVNC}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(&staticAssetRepo{asset: asset})
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		ctx := aitool.WithDocGate(context.Background(), aitool.NewDocGate())

		execTool := findExecTool(t, "exec")
		td := &agent.ToolDispatcher{
			Tools:      []agent.Tool{execTool},
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
		}
		td.Run(ctx, agent.DispatchInput{
			ToolName:  "exec",
			ToolUseID: "tu_unsupported",
			Input:     map[string]any{"asset": "78", "command": "whoami"},
		})

		entry := waitForAuditEntry(t, mockRepo, "exec", 78)
		So(entry.Success, ShouldEqual, 0)
		So(entry.Decision, ShouldEqual, "deny")
		So(entry.DecisionSource, ShouldEqual, aictx.SourceExecUnsupportedType)
	})
}

// TestAuditMiddleware_ExecPrecheckFailureIsDistinguishable covers the third
// short-circuit: a serial asset with no connected port (permission.PrecheckFunc).
func TestAuditMiddleware_ExecPrecheckFailureIsDistinguishable(t *testing.T) {
	Convey("precheck 失败的 exec 审计行要标出短路原因", t, func() {
		mockRepo := registerMockAuditRepo(t)

		asset := &asset_entity.Asset{ID: 79, Name: "console-79", Type: asset_entity.AssetTypeSerial}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(&staticAssetRepo{asset: asset})
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		// Mark serial documented so the call reaches the precheck rather than stopping at
		// the gate -- otherwise this would just re-test the gate path above.
		gate := aitool.NewDocGate()
		gate.MarkDocumented(0, asset_entity.AssetTypeSerial)
		ctx := aitool.WithDocGate(context.Background(), gate)
		ctx = helper.WithSerialManager(ctx, noSessionSerialManager{})

		execTool := findExecTool(t, "exec")
		td := &agent.ToolDispatcher{
			Tools:      []agent.Tool{execTool},
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{{Matcher: ".*", Fn: auditMiddleware}},
		}
		td.Run(ctx, agent.DispatchInput{
			ToolName:  "exec",
			ToolUseID: "tu_precheck",
			Input:     map[string]any{"asset": "79", "command": "display version"},
		})

		entry := waitForAuditEntry(t, mockRepo, "exec", 79)
		So(entry.Success, ShouldEqual, 0)
		So(entry.Decision, ShouldEqual, "deny")
		So(entry.DecisionSource, ShouldEqual, aictx.SourceExecPrecheckFailed)
	})
}

// noSessionSerialManager reports "no active session" for every asset, driving the
// serial PrecheckFunc's failure path.
type noSessionSerialManager struct{}

func (noSessionSerialManager) GetSessionByAssetID(_ int64) (serial_svc.CommandSession, bool) {
	return nil, false
}
