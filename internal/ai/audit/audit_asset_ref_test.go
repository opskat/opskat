package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

func TestWriteToolCall_RedactsSensitiveRequestFieldsRecursively(t *testing.T) {
	repo := setupAuditRepo(t)
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "put_asset",
		ArgsJSON: `{"name":"db","config":{"username":"admin","password":"db-pass","privateKey":"pem-body","nested":[{"api_key":"provider-key","authorization":"Bearer socket-token"}]}}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	request := repo.logs[0].Request
	for _, secret := range []string{"db-pass", "pem-body", "provider-key", "socket-token"} {
		if strings.Contains(request, secret) {
			t.Fatalf("audit request leaked %q: %s", secret, request)
		}
	}
	var stored struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal([]byte(request), &stored); err != nil {
		t.Fatalf("decode stored audit request: %v", err)
	}
	if stored.Config["username"] != "admin" || stored.Config["password"] != "<redacted>" {
		t.Fatalf("audit request lost safe context or redaction marker: %s", request)
	}
}

func TestWriteToolCall_RedactsSensitiveResultFieldsRecursively(t *testing.T) {
	repo := setupAuditRepo(t)
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "get_asset",
		ArgsJSON: `{"id":7}`,
		Result:   `{"id":7,"config":{"host":"db.internal","password":"result-pass","items":[{"refreshToken":"refresh-secret","secret_access_key":"oss-secret"}]}}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	result := repo.logs[0].Result
	for _, secret := range []string{"result-pass", "refresh-secret", "oss-secret"} {
		if strings.Contains(result, secret) {
			t.Fatalf("audit result leaked %q: %s", secret, result)
		}
	}
	if !strings.Contains(result, "db.internal") {
		t.Fatalf("audit result lost non-sensitive context: %s", result)
	}
}

// TestWriteToolCall_RedactsMatchedPatternAndAllTextColumns locks the audit/log sink
// closure (spec task 3): every text column persisted to audit_logs — including
// matched_pattern, which is the only one that used to be written verbatim — must go
// through the canonical redactor. The synthetic payload spans nested-secret, PEM,
// Authorization and signature/challenge/Agent-endpoint forms so a future column that
// skips the redactor fails here.
func TestWriteToolCall_RedactsMatchedPatternAndAllTextColumns(t *testing.T) {
	repo := setupAuditRepo(t)
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"db","command":"connect"}`,
		Command:  `client --password cmd-pass --signature cmd-sig`,
		Result:   "-----BEGIN PRIVATE KEY-----\nres-key-body\n-----END PRIVATE KEY-----",
		Error:    errors.New("Authorization: Bearer err-token; challenge=err-chal"),
		Decision: &aictx.CheckResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourcePolicyDeny,
			MatchedPattern: "kubectl --token=mt-token --signature mt-sig --agent-endpoint /tmp/ssh-9/agent.sock delete *",
		},
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	for column, value := range map[string]string{
		"command":         entry.Command,
		"result":          entry.Result,
		"error":           entry.Error,
		"matched_pattern": entry.MatchedPattern,
	} {
		for _, secret := range []string{"cmd-pass", "cmd-sig", "res-key-body", "err-token", "err-chal", "mt-token", "mt-sig"} {
			if strings.Contains(value, secret) {
				t.Fatalf("audit %s leaked %q: %s", column, secret, value)
			}
		}
	}
	// 安全 correlation 语义保留：决策字段与匹配来源不受脱敏影响。
	if entry.Decision != "deny" || entry.DecisionSource != aictx.SourcePolicyDeny {
		t.Fatalf("decision fields must survive redaction, got decision=%q source=%q", entry.Decision, entry.DecisionSource)
	}
}

func TestWriteToolCall_RedactsRecognizableSecretsFromTextColumns(t *testing.T) {
	repo := setupAuditRepo(t)
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"db","command":"connect"}`,
		Command:  `client --password command-pass --api-key=command-key`,
		Result:   "-----BEGIN PRIVATE KEY-----\nprivate-key-body\n-----END PRIVATE KEY-----",
		Error:    errors.New("request failed: Authorization: Bearer error-token"),
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	for column, value := range map[string]string{
		"command": entry.Command,
		"result":  entry.Result,
		"error":   entry.Error,
	} {
		for _, secret := range []string{"command-pass", "command-key", "private-key-body", "error-token"} {
			if strings.Contains(value, secret) {
				t.Fatalf("audit %s leaked %q: %s", column, secret, value)
			}
		}
	}
}

// memAuditRepo captures Create() calls synchronously. Unlike runner's
// mockAuditRepo (used behind the async auditMiddleware goroutine), WriteToolCall is
// called directly here, so no wait/lock dance is needed.
type memAuditRepo struct {
	logs []*audit_entity.AuditLog
}

func (m *memAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	m.logs = append(m.logs, log)
	return nil
}

func (m *memAuditRepo) List(_ context.Context, _ audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return m.logs, int64(len(m.logs)), nil
}

func (m *memAuditRepo) ListSessions(_ context.Context, _ int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

func setupAuditRepo(t *testing.T) *memAuditRepo {
	t.Helper()
	m := &memAuditRepo{}
	orig := audit_repo.Audit()
	audit_repo.RegisterAudit(m)
	// Restore unconditionally, including back to nil: an `if orig != nil` guard would
	// silently leave this test's exhausted mock as the process-wide default once the
	// test finished, for any later test relying on the original (or nil) registration
	// -- see setupExecAssetRepo in runner/audit_exec_asset_test.go, whose sibling guard
	// this mirrors.
	t.Cleanup(func() { audit_repo.RegisterAudit(orig) })
	return m
}

// TestWriteToolCall_PrefersPreResolvedAssetAttribution locks in the fix for the
// exec/help audit-attribution regression: those tools identify their asset via
// args["asset"] (a numeric id OR a name string), which aictx.ArgInt64 cannot parse,
// so the old args["asset_id"]/args["id"] lookup always produced AssetID=0 and
// AssetName="" for them. The resolution itself has to happen in runner's
// auditMiddleware (via assetref.Resolve, before the tool runs) because package audit
// can't import assetref/permission without an import cycle -- see ToolCallInfo's doc
// comment. This test locks in the receiving end: WriteToolCall must prefer
// info.AssetID/AssetName over parsing args when they're supplied.
func TestWriteToolCall_PrefersPreResolvedAssetAttribution(t *testing.T) {
	repo := setupAuditRepo(t)
	w := NewDefaultAuditWriter()

	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName:  "exec",
		ArgsJSON:  `{"asset":"web-1","command":"uptime"}`,
		Result:    "ok",
		AssetID:   7,
		AssetName: "web-1",
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 7 {
		t.Fatalf("AssetID = %d, want 7", entry.AssetID)
	}
	if entry.AssetName != "web-1" {
		t.Fatalf("AssetName = %q, want %q", entry.AssetName, "web-1")
	}
}

// TestWriteToolCall_PrefersPreResolvedCommand locks in the k8s canonicalization fix:
// exec's own extractor records the raw command for every asset type, including k8s,
// where the permission check and approval dialog instead see the
// --context/--namespace-injected effective command. When the caller supplies Command,
// WriteToolCall must use it verbatim instead of calling ExtractCommandForAudit.
func TestWriteToolCall_PrefersPreResolvedCommand(t *testing.T) {
	repo := setupAuditRepo(t)
	w := NewDefaultAuditWriter()

	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"k8s-1","command":"apply -f x"}`,
		Command:  "kubectl --context prod --namespace app apply -f x",
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	want := "kubectl --context prod --namespace app apply -f x"
	if got := repo.logs[0].Command; got != want {
		t.Fatalf("Command = %q, want %q", got, want)
	}
}

// TestWriteToolCall_FallsBackToArgsWhenNotPreResolved verifies the callers that don't
// pre-resolve still work: with AssetID/AssetName/Command left at their zero values,
// WriteToolCall must fall back to args["asset_id"]/args["id"] + ExtractCommandForAudit.
// Most opsctl tool calls are still in this group — only the exec and batch *verbs* install
// aictx.AuditCommandSlot (cmd/opsctl/command's writeOpsctlAudit reads it back; both write
// their rows under the tool name "exec"), while the put_asset/delete_asset/list/... calls
// never do. The AI runner similarly only fills Command in when it could resolve the asset.
func TestWriteToolCall_FallsBackToArgsWhenNotPreResolved(t *testing.T) {
	repo := setupAuditRepo(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })
	mockAsset.EXPECT().Find(gomock.Any(), int64(3)).Return(
		&asset_entity.Asset{ID: 3, Name: "db-1"}, nil)

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset_id":3,"command":"uptime"}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 3 || entry.AssetName != "db-1" {
		t.Fatalf("got AssetID=%d AssetName=%q, want 3/db-1", entry.AssetID, entry.AssetName)
	}
	if entry.Command != "uptime" {
		t.Fatalf("Command = %q, want %q", entry.Command, "uptime")
	}
}

// TestWriteToolCall_ResolvesNumericAssetRef locks the audit row's asset attribution for
// callers that don't pre-resolve. opsctl's exec/batch commands resolve the asset
// themselves and then hand the unified exec tool args["asset"]=<numeric id> — the
// per-type tools and verbs they replaced passed args["asset_id"], so before
// numericAssetRef every one of those audit rows silently landed with asset_id=0 and an
// empty asset_name. Nothing errors when that happens; the row just stops being
// attributable.
func TestWriteToolCall_ResolvesNumericAssetRef(t *testing.T) {
	repo := setupAuditRepo(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })
	mockAsset.EXPECT().Find(gomock.Any(), int64(9)).Return(
		&asset_entity.Asset{ID: 9, Name: "cache-1"}, nil)

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"9","command":"PING"}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 9 || entry.AssetName != "cache-1" {
		t.Fatalf("got AssetID=%d AssetName=%q, want 9/cache-1", entry.AssetID, entry.AssetName)
	}
}

// TestWriteToolCall_GroupScopedToolDoesNotMisattributeToAsset locks Important 4's fix:
// delete_group/put_group/get_group's numeric identifier is also spelled args["id"], but
// it names a *group*, not an asset — get_asset uses the very same key for an asset id.
// Before the fix, WriteToolCall's generic fallback blindly read args["id"] as an asset
// id and looked it up in asset_repo, so "delete group 3" would silently attribute to
// whatever asset happens to have id 3 (a completely unrelated row) whenever one exists.
// This test deliberately makes group id 3 and asset id 3 both exist, and asserts the
// fixed fallback refuses to conflate them: no EXPECT() is set on the asset mock, so if
// WriteToolCall ever queries it again, gomock fails the test outright.
func TestWriteToolCall_GroupScopedToolDoesNotMisattributeToAsset(t *testing.T) {
	repo := setupAuditRepo(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })
	// Deliberately no .EXPECT() on Find: a fixed WriteToolCall must never ask asset_repo
	// about a group-scoped tool's "id" at all.

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "delete_group",
		ArgsJSON: `{"id":3,"delete_assets":false}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 0 || entry.AssetName != "" {
		t.Fatalf("delete_group must not attribute to an asset, got AssetID=%d AssetName=%q "+
			"— the group id was misread as an asset id", entry.AssetID, entry.AssetName)
	}
}

// TestWriteToolCall_GetGroupDoesNotMisattributeToAsset extends
// TestWriteToolCall_GroupScopedToolDoesNotMisattributeToAsset's coverage to get_group.
// The groupScopedTools registry (extractor_default.go's init()) is pure opt-in: a tool
// name missing from it doesn't fail to compile and doesn't turn any *other* test red —
// before this test, get_group's registration was locked by nothing at all, so dropping
// its RegisterGroupScopedTool("get_group") call would silently resurrect the original
// misattribution bug for it while every other test kept passing. This pins
// WriteToolCall's actual behavior for get_group specifically.
func TestWriteToolCall_GetGroupDoesNotMisattributeToAsset(t *testing.T) {
	repo := setupAuditRepo(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })
	// Deliberately no .EXPECT() on Find: get_group's "id" names a group, so a correctly
	// registered WriteToolCall must never ask asset_repo about it.

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "get_group",
		ArgsJSON: `{"id":3}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 0 || entry.AssetName != "" {
		t.Fatalf("get_group must not attribute to an asset, got AssetID=%d AssetName=%q "+
			"— the group id was misread as an asset id", entry.AssetID, entry.AssetName)
	}
}

// TestWriteToolCall_PutGroupDoesNotMisattributeToAsset is put_group's sibling of
// TestWriteToolCall_GetGroupDoesNotMisattributeToAsset — see that test's comment for why
// each group-scoped tool needs its own lock rather than relying on delete_group's.
func TestWriteToolCall_PutGroupDoesNotMisattributeToAsset(t *testing.T) {
	repo := setupAuditRepo(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })
	// Deliberately no .EXPECT() on Find: put_group's "id" names a group, so a correctly
	// registered WriteToolCall must never ask asset_repo about it.

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "put_group",
		ArgsJSON: `{"id":3,"name":"renamed"}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 0 || entry.AssetName != "" {
		t.Fatalf("put_group must not attribute to an asset, got AssetID=%d AssetName=%q "+
			"— the group id was misread as an asset id", entry.AssetID, entry.AssetName)
	}
}

// TestWriteToolCall_NameAssetRefLeavesAssetUnresolved documents the deliberate limit of
// numericAssetRef: a NAME in args["asset"] needs assetref.Resolve, which package audit
// cannot import (permission already depends on audit, so the reverse edge would cycle).
// Callers that can resolve names — runner.auditMiddleware — must pre-fill AssetID.
// Guessing here (e.g. a name→id lookup by string match) would attribute audit rows to
// the wrong asset whenever names collide.
func TestWriteToolCall_NameAssetRefLeavesAssetUnresolved(t *testing.T) {
	repo := setupAuditRepo(t)

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"cache-1","command":"PING"}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	if got := repo.logs[0].AssetID; got != 0 {
		t.Fatalf("AssetID = %d, want 0 (a name is not resolvable here)", got)
	}
}
