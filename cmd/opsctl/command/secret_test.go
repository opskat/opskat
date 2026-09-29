package command

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// createOpsctlGrafanaAsset creates a generic asset directly through asset_repo (bypassing
// asset_svc.Create's default-policy injection, irrelevant to these tests since they stub
// secretApprovalFn) on the "grafana" custom type registered by setupGenericOpsctl
// (host non-secret required, token secret required). tokenPlain is encrypted the same way
// asset_put_svc would, mirroring setupGenericHTTPExec in exec_test.go.
func createOpsctlGrafanaAsset(ctx context.Context, name, host, tokenPlain string) error {
	cipher, err := credential_svc.Default().Encrypt(tokenPlain)
	if err != nil {
		return err
	}
	asset := &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeGeneric}
	if err := asset.SetGenericConfig(&asset_entity.GenericConfig{
		CustomType: "grafana",
		Values: map[string]asset_entity.GenericValue{
			"host":  {Value: host},
			"token": {Value: cipher},
		},
	}); err != nil {
		return err
	}
	return asset_repo.Asset().Create(ctx, asset)
}

// docs/specs/2026-09-28-generic-asset.md「取值」: `opsctl secret get <asset> <field>`
// shares its execution with the get_asset_secret AI tool via tool.AllToolDefs(); these
// tests drive cmdSecret directly, the same entry point Execute()'s "secret" case reaches.

// captureStdout mirrors captureStderr (exec_test.go) for the one command whose success
// output this task's spec pins down exactly ("prints the raw value followed by a
// newline") — stderr alone can't tell a pretty-printed JSON reformat from the raw value.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stdout = orig
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(data)
}

func setupOpsctlSecretHandlers(t *testing.T) map[string]tool.ToolHandlerFunc {
	t.Helper()
	return buildHandlerMap()
}

func TestCmdSecretGet_NonGenericAssetRejected(t *testing.T) {
	ctx := setupGenericOpsctl(t)
	require.NoError(t, asset_repo.Asset().Create(ctx, &asset_entity.Asset{Name: "web-1", Type: asset_entity.AssetTypeSSH}))
	handlers := setupOpsctlSecretHandlers(t)

	stderr := captureStderr(t, func() {
		code := cmdSecret(ctx, handlers, []string{"get", "web-1", "password"}, "")
		assert.Equal(t, 1, code)
	})
	assert.Contains(t, stderr, "generic")
}

func TestCmdSecretGet_UnknownFieldExitsWithClearError(t *testing.T) {
	ctx := setupGenericOpsctl(t)
	require.NoError(t, createOpsctlGrafanaAsset(ctx, "grafana-prod", "grafana.internal", "tok"))
	handlers := setupOpsctlSecretHandlers(t)

	stderr := captureStderr(t, func() {
		code := cmdSecret(ctx, handlers, []string{"get", "grafana-prod", "nope"}, "")
		assert.Equal(t, 1, code)
	})
	assert.Contains(t, stderr, "nope")
	assert.Contains(t, stderr, "host")
	assert.Contains(t, stderr, "token")
}

// TestCmdSecretGet_NonSecretFieldPrintsRawValueWithoutApproval locks "非密钥字段...直接
// 返回，不经过审批" on the opsctl side: secretApprovalFn must never be called, and stdout
// is exactly the raw value plus a newline (fmt.Println), not a JSON reformat.
func TestCmdSecretGet_NonSecretFieldPrintsRawValueWithoutApproval(t *testing.T) {
	ctx := setupGenericOpsctl(t)
	require.NoError(t, createOpsctlGrafanaAsset(ctx, "grafana-prod", "grafana.internal", "tok"))
	handlers := setupOpsctlSecretHandlers(t)

	origApproval := secretApprovalFn
	calls := 0
	secretApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		calls++
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	t.Cleanup(func() { secretApprovalFn = origApproval })

	stdout := captureStdout(t, func() {
		code := cmdSecret(ctx, handlers, []string{"get", "grafana-prod", "host"}, "")
		assert.Equal(t, 0, code)
	})
	assert.Equal(t, "grafana.internal\n", stdout)
	assert.Equal(t, 0, calls, "a non-secret field must never pop an approval dialog")
}

// TestCmdSecretGet_SecretFieldAllowedPrintsRawValue locks the allow half of the secret
// path, plus the approval request shape opsctl builds: match object secret:<field>,
// approval type "generic", and a detail mentioning the model provider.
func TestCmdSecretGet_SecretFieldAllowedPrintsRawValue(t *testing.T) {
	ctx := setupGenericOpsctl(t)
	// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
	secret := "glsa_opsctl_allowed"
	require.NoError(t, createOpsctlGrafanaAsset(ctx, "grafana-prod", "grafana.internal", secret))
	handlers := setupOpsctlSecretHandlers(t)

	origApproval := secretApprovalFn
	var gotReq approval.ApprovalRequest
	secretApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
		gotReq = req
		return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-secret"}, nil
	}
	t.Cleanup(func() { secretApprovalFn = origApproval })

	stdout := captureStdout(t, func() {
		code := cmdSecret(ctx, handlers, []string{"get", "grafana-prod", "token"}, "")
		assert.Equal(t, 0, code)
	})
	assert.Equal(t, secret+"\n", stdout)
	assert.Equal(t, "generic", gotReq.Type)
	assert.Equal(t, "secret:token", gotReq.Command)
	assert.Contains(t, gotReq.Detail, "model provider")
	assert.NotContains(t, gotReq.Detail, secret)
}

// TestCmdSecretGet_SecretFieldDeniedNeverPrintsValueAndAuditsWithoutIt locks the deny
// half: no value on stdout, a non-zero exit, and — via the same mockAuditWriter used by
// TestCallHandler_Decision — an audit row whose Result never carries the plaintext.
func TestCmdSecretGet_SecretFieldDeniedNeverPrintsValueAndAuditsWithoutIt(t *testing.T) {
	ctx := setupGenericOpsctl(t)
	// #nosec G101 -- intentional test fixture used to verify that secret field values never leak.
	secret := "glsa_opsctl_denied"
	require.NoError(t, createOpsctlGrafanaAsset(ctx, "grafana-prod", "grafana.internal", secret))
	handlers := setupOpsctlSecretHandlers(t)

	mockAudit := &mockAuditWriter{}
	origWriter := opsctlAuditWriter
	opsctlAuditWriter = mockAudit
	t.Cleanup(func() { opsctlAuditWriter = origWriter })

	origApproval := secretApprovalFn
	secretApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}, errors.New("operation denied: denied")
	}
	t.Cleanup(func() { secretApprovalFn = origApproval })

	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			code := cmdSecret(ctx, handlers, []string{"get", "grafana-prod", "token"}, "")
			assert.NotEqual(t, 0, code)
		})
		assert.NotContains(t, stderr, secret)
	})
	assert.Equal(t, "", stdout, "a denied secret get must print nothing on stdout")

	require.Len(t, mockAudit.calls, 1)
	info := mockAudit.lastCall()
	assert.Equal(t, "get_asset_secret", info.ToolName)
	assert.NotContains(t, info.Result, secret)
	assert.NotContains(t, info.ArgsJSON, secret)
}
