package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// opsctl batch with extension assets in it: an extension item joins the whole-batch
// policy check and the one approval the built-in items get, every approval surface
// shows its action and resource, it runs in opsctl's own process, and each extension
// is loaded once per batch. Everything below the command is real: the fixture
// extension's WASM guest, the SQLite data dir, the audit table. Built-in items run
// through a stub of the exec tool — what is under test is the batch around them.
//
// The fixture's default policy allows "read" only: `echo` runs without approval and
// `reject_host --resource=logs` (action write, resource logs) needs an approver, as
// does a kafka "message produce" (message.write).

const (
	batchExtWrite  = "reject_host --resource=logs"
	batchExtRead   = "echo --msg=hi"
	batchKafkaCall = "message produce orders"
	batchOSSCall   = "object list verify-batch"
)

type batchExtEnv struct {
	*localExtEnv
	kafka, oss  *asset_entity.Asset
	activations map[string]int
	mu          sync.Mutex
}

func setupBatchExt(t *testing.T, states ...*extension_state_entity.ExtensionState) *batchExtEnv {
	t.Helper()
	env := &batchExtEnv{localExtEnv: setupLocalExtExec(t, states...), activations: map[string]int{}}
	ctx := context.Background()
	env.kafka = &asset_entity.Asset{Name: "kf-1", Type: asset_entity.AssetTypeKafka, Status: asset_entity.StatusActive, Createtime: 1}
	require.NoError(t, asset_repo.Asset().Create(ctx, env.kafka))
	env.oss = &asset_entity.Asset{Name: "oss-1", Type: asset_entity.AssetTypeOSS, Status: asset_entity.StatusActive, Createtime: 1}
	require.NoError(t, asset_repo.Asset().Create(ctx, env.oss))
	swapGlobal(t, func() func(context.Context, string) error { return activateExtensionFn },
		func(f func(context.Context, string) error) { activateExtensionFn = f },
		func(ctx context.Context, name string) error {
			env.mu.Lock()
			env.activations[name]++
			env.mu.Unlock()
			return activateExtension(ctx, name)
		})
	return env
}

// batch runs `opsctl batch <args>` and returns its exit code, decoded output and
// stderr. The exec tool is the real one for extension items and a stub answering
// "builtin-ok" for the built-in assets.
func (e *batchExtEnv) batch(t *testing.T, args ...string) (int, batchOutput, string) {
	t.Helper()
	return e.batchWithMFACode(t, "", args...)
}

// batchWithMFACode is batch with a global --mfa-code ("" = none).
func (e *batchExtEnv) batchWithMFACode(t *testing.T, mfaCode string, args ...string) (int, batchOutput, string) {
	t.Helper()
	handlers := buildHandlerMap()
	execTool := handlers[batchAuditTool]
	builtin := map[string]bool{strconv.FormatInt(e.kafka.ID, 10): true, strconv.FormatInt(e.oss.ID, 10): true}
	handlers[batchAuditTool] = func(ctx context.Context, params map[string]any) (string, error) {
		if builtin[params["asset"].(string)] {
			return "builtin-ok", nil
		}
		return execTool(ctx, params)
	}
	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = cmdBatch(withMFACode(context.Background(), mfaCode), handlers, args, "sess-batch")
		})
	})
	var out batchOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &out), "stdout: %s\nstderr: %s", stdout, stderr)
	return code, out, stderr
}

func (e *batchExtEnv) item(a *asset_entity.Asset, command string) string {
	return a.Name + ":" + command
}

func auditRowFor(t *testing.T, rows []audit_entity.AuditLog, assetID int64, command string) audit_entity.AuditLog {
	t.Helper()
	for _, r := range rows {
		if r.AssetID == assetID && r.Command == command {
			return r
		}
	}
	require.Failf(t, "no audit row", "asset %d command %q in %+v", assetID, command, rows)
	return audit_entity.AuditLog{}
}

// Extension items and a built-in item needing approval are approved together in one
// terminal prompt that shows the extension item's action and resource and offers no
// "allow always"; the read call the policy allows runs without being listed; every
// item runs, the extension once loaded, and each is audited as a batch item.
func TestBatchExt_MixedBatchIsApprovedOnceAndRuns(t *testing.T) {
	env := setupBatchExt(t)
	prompts := interactiveTerminal(t, "a\n")

	code, out, stderr := env.batch(t,
		env.item(env.asset, batchExtWrite), env.item(env.kafka, batchKafkaCall), env.item(env.asset, batchExtRead))

	require.Equal(t, 0, code, "stderr: %s\nprompt: %s", stderr, prompts)
	require.Len(t, out.Results, 3)
	for _, r := range out.Results {
		assert.Empty(t, r.Error, "%+v", r)
		assert.Equal(t, 0, r.ExitCode, "%+v", r)
	}
	assert.JSONEq(t, `{"ran":true}`, out.Results[0].Stdout)
	assert.Equal(t, "builtin-ok", out.Results[1].Stdout)
	assert.Contains(t, out.Results[2].Stdout, `"msg":"hi"`)

	prompt := prompts.String()
	assert.Equal(t, 1, strings.Count(prompt, "Batch approval required"), "one approval for the whole batch: %s", prompt)
	assert.Contains(t, prompt, "Batch approval required (2 items)")
	assert.Contains(t, prompt, "Action: write")
	assert.Contains(t, prompt, "Resource: logs")
	assert.Contains(t, prompt, "      - write:logs", "the extension item's subject is its rule, not the command")
	assert.Contains(t, prompt, "      - message.write orders", "the built-in item is listed as before")
	assert.NotContains(t, prompt, "echo", "a call the policy allows is not asked about")
	assert.Contains(t, prompt, "[a] allow all   [d] deny all")
	assert.NotContains(t, prompt, "[p]", "batch has no allow always")

	assert.Equal(t, map[string]int{extensiontest.FixtureName: 1}, env.activations, "one load per extension per batch")

	rows := env.auditRows(t)
	require.Len(t, rows, 3)
	write := auditRowFor(t, rows, env.asset.ID, batchExtWrite)
	assert.Equal(t, "opsctl", write.Source)
	assert.Equal(t, batchAuditTool, write.ToolName)
	assert.Equal(t, batchArgsJSON(env.asset.ID, batchExtWrite, ""), write.Request)
	assert.Equal(t, "allow", write.Decision)
	assert.Equal(t, aictx.SourceUserAllow, write.DecisionSource)
	assert.Equal(t, 1, write.Success)
	assert.Equal(t, aictx.SourceUserAllow, auditRowFor(t, rows, env.kafka.ID, "message.write orders").DecisionSource)
	assert.Equal(t, "allow", auditRowFor(t, rows, env.asset.ID, batchExtRead).Decision)
}

// An item whose extension is disabled or cannot be loaded fails alone, with the
// reason and an audit row of the batch's own format, while the rest of the batch
// runs; the extension is tried once however many items it has.
func TestBatchExt_UnusableExtensionFailsOnlyItsItems(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T) *batchExtEnv
		reason string
	}{
		{"disabled", func(t *testing.T) *batchExtEnv {
			return setupBatchExt(t, &extension_state_entity.ExtensionState{Name: extensiontest.FixtureName, Enabled: false})
		}, "disabled"},
		{"load failure", func(t *testing.T) *batchExtEnv {
			env := setupBatchExt(t)
			require.NoError(t, os.Remove(filepath.Join(env.extDir, "main.wasm")))
			return env
		}, "main.wasm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.setup(t)

			code, out, stderr := env.batch(t,
				env.item(env.asset, batchExtRead), env.item(env.oss, batchOSSCall), env.item(env.asset, batchExtWrite))

			require.Equal(t, 0, code, "the built-in item ran: %s", stderr)
			require.Len(t, out.Results, 3)
			for _, i := range []int{0, 2} {
				assert.Contains(t, out.Results[i].Error, `"fixture-ext"`)
				assert.Contains(t, out.Results[i].Error, tc.reason)
				assert.Equal(t, -1, out.Results[i].ExitCode)
				assert.Equal(t, env.asset.ID, out.Results[i].AssetID)
			}
			assert.Empty(t, out.Results[1].Error)
			assert.Equal(t, "builtin-ok", out.Results[1].Stdout)
			assert.Equal(t, map[string]int{extensiontest.FixtureName: 1}, env.activations, "the extension is tried once")

			rows := env.auditRows(t)
			require.Len(t, rows, 3)
			for _, command := range []string{batchExtRead, batchExtWrite} {
				row := auditRowFor(t, rows, env.asset.ID, command)
				assert.Equal(t, batchAuditTool, row.ToolName)
				assert.Equal(t, batchArgsJSON(env.asset.ID, command, ""), row.Request)
				assert.Equal(t, 0, row.Success)
				assert.Contains(t, row.Error, tc.reason)
			}
		})
	}
}

// With nobody to ask, NEEDS AUTHORIZATION lists every item: the extension item by
// its action and resource, with the policy allow line of its rule; the built-in
// item exactly as before.
func TestBatchExt_NeedsAuthorizationNamesTheExtensionRule(t *testing.T) {
	env := setupBatchExt(t)

	code, out, stderr := env.batch(t, env.item(env.asset, batchExtWrite), env.item(env.kafka, batchKafkaCall))

	require.Equal(t, refusalExitCode, code, "stderr: %s", stderr)
	firstLine, _, _ := strings.Cut(stderr, "\n")
	assert.Equal(t, needsAuthorizationMarker, firstLine)
	assert.Contains(t, stderr, "  - write:logs")
	assert.Contains(t, stderr, "opsctl policy allow "+strconv.FormatInt(env.asset.ID, 10)+" -- 'write:logs'")
	assert.Contains(t, stderr, "opsctl policy allow "+strconv.FormatInt(env.kafka.ID, 10)+" -- 'message.write orders'")
	assert.NotContains(t, stderr, "-- '"+batchExtWrite+"'", "the whole command is no rule an extension accepts")
	for _, r := range out.Results {
		assert.Contains(t, r.Error, "approval failed")
	}
}

// With no terminal and the desktop app running, the one batch request it is sent
// carries each extension item's action and resources (built-in items none), and the
// approved batch runs.
func TestBatchExt_DesktopRequestCarriesTheClassification(t *testing.T) {
	env := setupBatchExt(t)
	// A desktop app of this test's own, on a data dir of its own: nothing here may
	// reach the approval socket of a real data dir. The directory is short — a unix
	// socket path has a small length limit.
	dir, err := os.MkdirTemp("", "opsctl-batch-ext-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NotEqual(t, os.Getenv("OPSKAT_DATA_DIR"), dir)
	swapGlobal(t, func() func() string { return approvalDataDir }, func(f func() string) { approvalDataDir = f },
		func() string { return dir })
	swapGlobal(t, func() func(string) error { return dialApprovalSocket }, func(f func(string) error) { dialApprovalSocket = f },
		func(string) error { return nil })

	var got []approval.ApprovalRequest
	server := approval.NewServer(func(_ context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
		got = append(got, req)
		return approval.ApprovalResponse{Approved: true}
	}, "")
	require.NoError(t, server.Start(approval.SocketPath(dir)))
	t.Cleanup(server.Stop)

	code, out, stderr := env.batch(t, env.item(env.asset, batchExtWrite), env.item(env.kafka, batchKafkaCall))

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.JSONEq(t, `{"ran":true}`, out.Results[0].Stdout)
	assert.Equal(t, "builtin-ok", out.Results[1].Stdout)
	require.Len(t, got, 1)
	require.Equal(t, "batch", got[0].Type)
	require.Len(t, got[0].BatchItems, 2)
	ext, builtin := got[0].BatchItems[0], got[0].BatchItems[1]
	assert.Equal(t, "fixture", ext.Type)
	assert.Equal(t, batchExtWrite, ext.Command)
	assert.Equal(t, "write", ext.Action)
	assert.Equal(t, "logs", ext.Resource)
	assert.Equal(t, []string{"logs"}, ext.Resources)
	assert.Equal(t, approval.BatchItem{
		Type: "kafka", AssetID: env.kafka.ID, AssetName: env.kafka.Name, Command: "message.write orders",
	}, builtin, "a built-in item is sent as before")
}

// An extension item reaches its endpoint through its asset's SSH tunnel the way
// `opsctl exec` on it does: the hop's MFA challenge is answered from the batch's
// --mfa-code, once.
func TestBatchExt_ExtensionItemThroughMFAHopAnswersWithMFACode(t *testing.T) {
	env := setupBatchExt(t)
	hop := putBehindMFAHop(t, env.localExtEnv, "123456")

	code, out, stderr := env.batchWithMFACode(t, "123456", env.item(env.asset, hop.tcpEchoCommand()))

	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.Len(t, out.Results, 1)
	assert.JSONEq(t, `{"echo":"ping"}`, out.Results[0].Stdout)
	assert.Equal(t, int32(1), hop.hop.challenges.Load())
}

// With no answer source the extension item's hop ends the batch like an SSH item
// that needs MFA: NEEDS MFA on stderr's first line, exit code 3.
func TestBatchExt_ExtensionItemThroughMFAHopWithoutSourceNeedsMFA(t *testing.T) {
	env := setupBatchExt(t)
	hop := putBehindMFAHop(t, env.localExtEnv, "123456")

	code, out, stderr := env.batch(t, env.item(env.asset, hop.tcpEchoCommand()))

	assert.Equal(t, refusalExitCode, code, "stderr: %s", stderr)
	require.Len(t, out.Results, 1)
	assert.NotEmpty(t, out.Results[0].Error)
	firstLine, _, _ := strings.Cut(stderr, "\n")
	assert.Equal(t, needsMFAMarker, firstLine)
}
