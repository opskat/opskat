package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// opsctl's approval chain on an extension asset: the same chain built-in types take
// (policy → terminal → desktop → NEEDS AUTHORIZATION), speaking the extension's own
// language — the (action, resource) its check_policy classified the call as, and
// rules / grants of the form <action>[:<resource-glob>]. Everything below the
// command is real: the fixture extension's WASM guest, the SQLite data dir, the rule
// and grant stores. The fixture's default policy allows "read" only, so
// `reject_host --resource=logs` (action write, resource logs) needs an approver.

var extWriteCall = []string{"--", "reject_host", "--resource=logs"}

// nextCommand puts the process in the state a new opsctl invocation starts from:
// the extension activated by the previous command released, and the boot-time
// describe-only registration redone.
func nextCommand(t *testing.T) {
	t.Helper()
	releaseExtensions(context.Background())
	registerExtensionAssetTypes()
}

// interactiveTerminal makes opsctl see stdin and stderr as terminals and answers the
// terminal prompt with answer; the second-confirmation of a permanent rule write is
// answered yes. It returns what the prompts wrote.
func interactiveTerminal(t *testing.T, answer string) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	swapGlobal(t, func() func() bool { return stdinIsTerminal }, func(f func() bool) { stdinIsTerminal = f }, func() bool { return true })
	swapGlobal(t, func() func() bool { return stderrIsTerminal }, func(f func() bool) { stderrIsTerminal = f }, func() bool { return true })
	swapGlobal(t, func() func() (io.Reader, io.Writer) { return terminalApprovalStreams },
		func(f func() (io.Reader, io.Writer)) { terminalApprovalStreams = f },
		func() (io.Reader, io.Writer) { return strings.NewReader(answer), &out })
	swapGlobal(t, func() func() (io.Reader, io.Writer) { return policyConfirmStreams },
		func(f func() (io.Reader, io.Writer)) { policyConfirmStreams = f },
		func() (io.Reader, io.Writer) { return strings.NewReader("y\n"), &out })
	return &out
}

// nonInteractive puts opsctl back to no terminal (the desktop stays unreachable, as
// setupLocalExtExec left it).
func nonInteractive(t *testing.T) {
	t.Helper()
	swapGlobal(t, func() func() bool { return stdinIsTerminal }, func(f func() bool) { stdinIsTerminal = f }, func() bool { return false })
	swapGlobal(t, func() func() bool { return stderrIsTerminal }, func(f func() bool) { stderrIsTerminal = f }, func() bool { return false })
}

// A call the tool refuses (RejectArgs) is denied at once — no approver is asked, not
// even an interactive terminal that would allow it — and audited as a policy deny
// carrying the tool's reason.
func TestExtApproval_RejectedArgumentsAreDeniedWithoutApprovalAndAudited(t *testing.T) {
	env := setupLocalExtExec(t)
	prompts := interactiveTerminal(t, "a\n")

	code, stdout, stderr := env.exec(t, "fx-1", "--", "reject_host", "--resource=http://evil/x")

	const reason = `resource "http://evil/x" must not name a host`
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, reason)
	assert.Empty(t, prompts.String(), "a call the tool refuses must not ask anyone")
	rows := env.auditRows(t)
	require.Len(t, rows, 1)
	assert.Equal(t, "opsctl", rows[0].Source)
	assert.Equal(t, "deny", rows[0].Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, rows[0].DecisionSource)
	assert.Equal(t, 0, rows[0].Success)
	assert.Contains(t, rows[0].Error, reason, "the audit row shows why the call was refused")
	assert.Equal(t, "reject_host --resource=http://evil/x", rows[0].Command)
}

// The terminal prompt shows the extension's action and resource, and "always allow"
// writes the extension rule — after which the same call runs with nobody to ask.
func TestExtApproval_TerminalAllowAlwaysWritesTheExtensionRule(t *testing.T) {
	env := setupLocalExtExec(t)
	prompts := interactiveTerminal(t, "p\n")

	code, stdout, stderr := env.exec(t, append([]string{"fx-1"}, extWriteCall...)...)

	require.Equal(t, 0, code, "stderr: %s\nprompt: %s", stderr, prompts)
	assert.JSONEq(t, `{"ran":true}`, stdout)
	prompt := prompts.String()
	assert.Contains(t, prompt, "Action: write")
	assert.Contains(t, prompt, "Resource: logs")
	assert.Contains(t, prompt, "  - write:logs", "the subject is the rule \"always allow\" writes")
	assert.Contains(t, prompt, `"tool": "reject_host"`, "the detail is the tool call the desktop shows")
	assert.Contains(t, prompt, "[p]")

	nextCommand(t)
	nonInteractive(t)
	code, stdout, stderr = env.exec(t, append([]string{"fx-1"}, extWriteCall...)...)
	require.Equal(t, 0, code, "the rule must let the same call through: %s", stderr)
	assert.JSONEq(t, `{"ran":true}`, stdout)

	nextCommand(t)
	code, _, stderr = env.exec(t, "fx-1", "--", "reject_host", "--resource=metrics")
	assert.Equal(t, refusalExitCode, code, "the rule covers the approved resource only: %s", stderr)
}

// With nobody to ask, NEEDS AUTHORIZATION names the extension rule; running the
// printed line and retrying lets the call through.
func TestExtApproval_NeedsAuthorizationLineWorksWhenFollowed(t *testing.T) {
	env := setupLocalExtExec(t)

	code, stdout, stderr := env.exec(t, append([]string{"fx-1"}, extWriteCall...)...)

	require.Equal(t, refusalExitCode, code, "stderr: %s", stderr)
	assert.Empty(t, stdout)
	firstLine, _, _ := strings.Cut(stderr, "\n")
	assert.Equal(t, needsAuthorizationMarker, firstLine)
	want := "opsctl policy allow " + strconv.FormatInt(env.asset.ID, 10) + " -- 'write:logs'"
	require.Contains(t, stderr, want)

	nextCommand(t)
	interactiveTerminal(t, "")
	var policyCode int
	policyErr := captureStderr(t, func() {
		captureStdout(t, func() {
			policyCode = cmdPolicy(context.Background(), shellSplitOpsctlLine(t, want), "sess-ext")
		})
	})
	require.Equal(t, 0, policyCode, "the printed line must run as is: %s", policyErr)

	nextCommand(t)
	nonInteractive(t)
	code, stdout, stderr = env.exec(t, append([]string{"fx-1"}, extWriteCall...)...)
	require.Equal(t, 0, code, "after following the line the call runs: %s", stderr)
	assert.JSONEq(t, `{"ran":true}`, stdout)
}

// A grant in the command's session — the shape the desktop's "always allow" and
// opsctl grant leave, ext:<policyType>:<action>:<resource> — lets the call through
// with no approver, and is audited as a grant allow.
func TestExtApproval_SessionGrantLetsTheCallThrough(t *testing.T) {
	env := setupLocalExtExec(t)
	ctx := context.Background()
	require.NoError(t, grant_repo.Grant().CreateSession(ctx, &grant_entity.GrantSession{
		ID: "sess-ext", Status: grant_entity.GrantStatusApproved, Createtime: time.Now().Unix(),
	}))
	require.NoError(t, grant_repo.Grant().CreateItems(ctx, []*grant_entity.GrantItem{{
		GrantSessionID: "sess-ext", ToolName: "fixture", AssetID: env.asset.ID, AssetName: env.asset.Name,
		Command: "ext:fixture:write:logs", Createtime: time.Now().Unix(),
	}}))

	code, stdout, stderr := env.exec(t, append([]string{"fx-1"}, extWriteCall...)...)

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.JSONEq(t, `{"ran":true}`, stdout)
	rows := env.auditRows(t)
	require.Len(t, rows, 1)
	assert.Equal(t, aictx.SourceGrantAllow, rows[0].DecisionSource)
	assert.Equal(t, "sess-ext", rows[0].SessionID)
}

// With no terminal and the desktop app running, the request it is sent carries the
// shared approval item's classification — what its dialog shows and what its
// "always allow" saves — and the approved call's result lands on opsctl's stdout.
func TestExtApproval_DesktopRequestCarriesTheClassification(t *testing.T) {
	env := setupLocalExtExec(t)
	// A desktop app of this test's own, on a data dir of its own: nothing here may
	// reach the approval socket of a real data dir. The directory is short — a unix
	// socket path has a small length limit.
	dir, err := os.MkdirTemp("", "opsctl-ext-approval-")
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

	code, stdout, stderr := env.exec(t, append([]string{"fx-1"}, extWriteCall...)...)

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.JSONEq(t, `{"ran":true}`, stdout)
	require.Len(t, got, 1)
	req := got[0]
	assert.Equal(t, "fixture", req.Type)
	assert.Equal(t, env.asset.ID, req.AssetID)
	assert.Equal(t, "reject_host --resource=logs", req.Command)
	assert.Equal(t, "write", req.Action)
	assert.Equal(t, "logs", req.Resource)
	assert.Equal(t, []string{"logs"}, req.Resources)
	assert.Equal(t, "write:logs", req.RememberPattern)
	var detail struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	require.NoError(t, json.Unmarshal([]byte(req.Detail), &detail), "the dialog renders an extension item's detail as the tool call: %q", req.Detail)
	assert.Equal(t, "reject_host", detail.Tool)
	assert.JSONEq(t, `{"resource":"logs"}`, string(detail.Args))
}

// An extension call its extension could not classify has no rule an approver could
// write or a human could pre-authorize: the terminal offers allow once / deny only,
// and with nobody to ask the refusal is NEEDS TTY rather than a policy allow line
// that would never match.
func TestExtApproval_UnclassifiedCallHasNoRuleToOffer(t *testing.T) {
	const typ = "unclassified-ext-approval-test"
	require.NoError(t, permission.RegisterPolicyCheck(typ,
		func(context.Context, int64, string) aictx.CheckResult {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		},
		func(context.Context, string) (permission.ExtensionClassification, bool) {
			return permission.ExtensionClassification{}, false
		}))
	t.Cleanup(func() { permission.UnregisterPolicyCheck(typ) })
	preserveTTYSeams(t)
	req := withApprovalItem(context.Background(),
		approval.ApprovalRequest{Type: typ, AssetID: 4, AssetName: "x-1", Command: "broken --x", SessionID: "sess-u"})

	var out bytes.Buffer
	res, err := runTTYApproval(context.Background(), req, strings.NewReader("p\na\n"), &out)
	require.NoError(t, err)
	assert.Equal(t, aictx.Allow, res.Decision)
	assert.NotContains(t, out.String(), "[p]")
	assert.Contains(t, out.String(), "unrecognized input", "allow always is not a choice here")

	_, err = refuseApproval(context.Background(), req, nil, "")
	require.Error(t, err)
	firstLine, _, _ := strings.Cut(err.Error(), "\n")
	assert.Equal(t, needsTTYMarker, firstLine)
	assert.NotContains(t, err.Error(), "policy allow")
}
