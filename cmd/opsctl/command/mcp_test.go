package command

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	cagotool "github.com/cago-frame/agents/tool"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/mcp"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

const testCLISecret = "cli-strong-secret-0123456789abcdef"

type lockedBufferWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedBufferWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func TestCmdMCPExternalModeRequiresControlSocketAndStrongSecret(t *testing.T) {
	t.Setenv(mcpApprovalSecretEnv, "")
	origSessionDir := sessionDataDir
	sessionDataDir = t.TempDir
	t.Cleanup(func() { sessionDataDir = origSessionDir })

	// Missing --approval-control-socket
	stderr := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--approval-mode=external"})
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr, "--approval-control-socket")

	// Missing / weak secret
	sock := filepath.Join(t.TempDir(), "ctrl.sock")
	t.Setenv(mcpApprovalSecretEnv, "short")
	stderr2 := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--approval-mode=external", "--approval-control-socket=" + sock})
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr2, mcpApprovalSecretEnv)
}

func TestCmdMCPRejectsInvalidFlagsAndExcessiveTTL(t *testing.T) {
	stderr := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--approval-mode=bogus"})
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr, "invalid --approval-mode")

	stderr2 := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--toolset=bogus"})
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr2, "invalid --toolset")

	stderr3 := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--approval-ttl=5h"})
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr3, "--approval-ttl")
}

func TestCmdMCPExternalCoreToolsetExcludesResolveAndAudit(t *testing.T) {
	t.Setenv(mcpApprovalSecretEnv, testCLISecret)
	origSessionDir := sessionDataDir
	sessionDir := t.TempDir()
	sessionDataDir = func() string { return sessionDir }
	t.Cleanup(func() { sessionDataDir = origSessionDir })
	// Seed a CLI active session and verify cmdMCP never overwrites or relies on it.
	require.NoError(t, writeActiveSession("cli-existing-session"))

	sock := filepath.Join(t.TempDir(), "mcp-ctrl.sock")
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	var stderrMu sync.Mutex
	var stderrBuf bytes.Buffer
	stderrWriter := &lockedBufferWriter{mu: &stderrMu, buf: &stderrBuf}
	origStdio := mcpStdio
	origStdout := mcpStdout
	mcpStdio = func() (io.Reader, io.Writer) { return serverR, stderrWriter }
	mcpStdout = func() io.Writer { return serverW }
	t.Cleanup(func() {
		mcpStdio = origStdio
		mcpStdout = origStdout
	})

	done := make(chan int, 1)
	go func() {
		done <- cmdMCP(context.Background(), []string{
			"--approval-mode=external",
			"--approval-control-socket=" + sock,
			"--toolset=core",
		})
	}()

	reader := bufio.NewReader(clientR)
	send := func(id int, method string, params any) mcp.Response {
		t.Helper()
		rawID, err := json.Marshal(id)
		require.NoError(t, err)
		var rawParams json.RawMessage
		if params != nil {
			rawParams, err = json.Marshal(params)
			require.NoError(t, err)
		}
		b, err := json.Marshal(mcp.Request{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams})
		require.NoError(t, err)
		_, err = clientW.Write(append(b, '\n'))
		require.NoError(t, err)
		line, err := reader.ReadBytes('\n')
		require.NoError(t, err)
		var resp mcp.Response
		require.NoError(t, json.Unmarshal(line, &resp))
		return resp
	}

	initResp := send(1, "initialize", map[string]any{"protocolVersion": mcp.ProtocolVersion})
	require.Nil(t, initResp.Error)

	// Verify OPSKAT_MCP_CONTROL_ADDR was emitted on stderr with platform IPC address.
	stderrMu.Lock()
	stderrOut := stderrBuf.String()
	stderrMu.Unlock()
	require.Contains(t, stderrOut, "OPSKAT_MCP_CONTROL_ADDR=")
	var controlAddr string
	for _, line := range strings.Split(stderrOut, "\n") {
		if strings.HasPrefix(line, "OPSKAT_MCP_CONTROL_ADDR=") {
			controlAddr = strings.TrimSpace(strings.TrimPrefix(line, "OPSKAT_MCP_CONTROL_ADDR="))
		}
	}
	require.NotEmpty(t, controlAddr)
	if runtime.GOOS == "windows" {
		require.True(t, strings.HasPrefix(controlAddr, `\\.\pipe\opskat-`), "expected Windows named pipe address, got %q", controlAddr)
	} else {
		require.Equal(t, sock, controlAddr)
	}

	listResp := send(2, "tools/list", nil)
	require.Nil(t, listResp.Error)
	raw, err := json.Marshal(listResp.Result)
	require.NoError(t, err)
	var listRes struct {
		Tools []mcp.ToolSpec `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &listRes))

	names := make(map[string]mcp.ToolSpec)
	for _, spec := range listRes.Tools {
		names[spec.Name] = spec
	}
	require.LessOrEqual(t, len(listRes.Tools), 10)
	require.Contains(t, names, "exec")
	require.Contains(t, names, "batch_exec")
	require.Contains(t, names, "cp")
	require.Contains(t, names, "request_permission")
	require.Contains(t, names, mcp.GetTaskToolName)
	require.NotContains(t, names, "_approval_resolve", "approval control plane must never appear in tools/list")
	require.NotContains(t, names, "list_audit", "list_audit must stay out of core toolset")

	// Verify MCP 2025-03-26 tool annotations are populated on tools/list
	require.NotNil(t, names["list_assets"].Annotations)
	require.NotNil(t, names["list_assets"].Annotations.ReadOnlyHint)
	require.True(t, *names["list_assets"].Annotations.ReadOnlyHint)
	require.NotNil(t, names[mcp.GetTaskToolName].Annotations)
	require.NotNil(t, names[mcp.GetTaskToolName].Annotations.ReadOnlyHint)
	require.True(t, *names[mcp.GetTaskToolName].Annotations.ReadOnlyHint)
	require.NotNil(t, names["exec"].Annotations)
	require.NotNil(t, names["exec"].Annotations.ReadOnlyHint)
	require.False(t, *names["exec"].Annotations.ReadOnlyHint)
	require.NotNil(t, names["exec"].Annotations.DestructiveHint)
	require.True(t, *names["exec"].Annotations.DestructiveHint)

	// Control socket must be reachable with valid secret
	pending, err := mcp.ListPendingViaControlSocket(context.Background(), sock, testCLISecret)
	require.NoError(t, err)
	require.Empty(t, pending)

	// CLI active session file remained untouched
	require.Equal(t, "cli-existing-session", resolveSessionID())

	_ = clientW.Close()
	_ = clientR.Close()
	select {
	case code := <-done:
		require.Equal(t, 0, code)
	case <-time.After(3 * time.Second):
		t.Fatal("cmdMCP did not exit after stdin closed")
	}
}

func TestGuardedPutAssetAndPutGroupRequireConfirmationAndMaskSecrets(t *testing.T) {
	called := false
	innerPutAsset := &cagotool.RawTool{
		NameStr:  "put_asset",
		IsSerial: true,
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			called = true
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}}, nil
		},
	}
	wrapped := &guardedPutAssetTool{inner: innerPutAsset}
	require.True(t, wrapped.Serial())

	var seenKind string
	var seenItems []permission.ApprovalItem
	decision := "deny"
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
		seenKind = kind
		seenItems = items
		return permission.ApprovalResponse{Decision: decision}
	})
	ctx := permission.WithPolicyChecker(context.Background(), checker)

	input := map[string]any{
		"name":     "prod-db",
		"type":     "ssh",
		"group_id": float64(5),
		"config": map[string]any{
			"host":     "10.0.0.5",
			"password": "SUPER_SECRET_PASSWORD_123",
		},
	}

	// Denied -> inner handler must NOT run, and approval detail must NEVER leak secret values
	res, err := wrapped.Call(ctx, input)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.False(t, called)
	require.Equal(t, permission.ApprovalKindOnce, seenKind)
	require.Len(t, seenItems, 1)
	require.NotContains(t, seenItems[0].Detail, "SUPER_SECRET_PASSWORD_123")
	require.Contains(t, seenItems[0].Detail, `"config_keys":["host","password"]`)

	// Allowed -> inner handler executes
	decision = "allow"
	res2, err := wrapped.Call(ctx, input)
	require.NoError(t, err)
	require.False(t, res2.IsError)
	require.True(t, called)

	// put_group also requires one-shot confirmation
	groupCalled := false
	wrappedGroup := &guardedPutGroupTool{inner: &cagotool.RawTool{
		NameStr: "put_group",
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			groupCalled = true
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}}, nil
		},
	}}
	decision = "deny"
	resG, err := wrappedGroup.Call(ctx, map[string]any{"id": float64(2), "parent_id": float64(9)})
	require.NoError(t, err)
	require.True(t, resG.IsError)
	require.False(t, groupCalled)
	require.Equal(t, permission.ApprovalKindOnce, seenKind)
}

func TestGuardedCpToolEnforcesLocalDirWhitelistAndUploadConfirmation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().FindByName(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(&asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	insideFile := filepath.Join(allowedDir, "app.log")
	outsideFile := filepath.Join(outsideDir, "secret.key")
	require.NoError(t, os.WriteFile(insideFile, []byte("ok"), 0o600))
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0o600))

	normDirs, err := normalizeAllowedLocalDirs([]string{allowedDir})
	require.NoError(t, err)

	called := false
	innerCp := &cagotool.RawTool{
		NameStr:  "cp",
		IsSerial: true,
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			called = true
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "copied"}}}, nil
		},
	}

	// 1. With --allow-local-dir: outside file is blocked (both upload and download)
	whitelisted := &guardedCpTool{inner: innerCp, allowedLocalDirs: normDirs}
	_, err = whitelisted.Call(context.Background(), map[string]any{"src": outsideFile, "dst": "1:/tmp/x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked by --allow-local-dir")
	require.False(t, called)

	_, err = whitelisted.Call(context.Background(), map[string]any{"src": "1:/var/log/a", "dst": outsideFile})
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked by --allow-local-dir")
	require.False(t, called)

	// Inside allowed dir proceeds to inner cp
	res, err := whitelisted.Call(context.Background(), map[string]any{"src": insideFile, "dst": "1:/tmp/x"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.True(t, called)

	// 2. Without --allow-local-dir: local upload requires explicit one-shot confirmation
	called = false
	var prompted bool
	confirmDecision := "deny"
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
		prompted = true
		require.Equal(t, permission.ApprovalKindOnce, kind)
		require.Len(t, items, 1)
		require.Equal(t, "local_read", items[0].Type)
		return permission.ApprovalResponse{Decision: confirmDecision}
	})
	unrestricted := &guardedCpTool{inner: innerCp}
	ctx := permission.WithPolicyChecker(context.Background(), checker)

	denyRes, err := unrestricted.Call(ctx, map[string]any{"src": outsideFile, "dst": "1:/tmp/loot"})
	require.NoError(t, err)
	require.True(t, denyRes.IsError)
	require.True(t, prompted)
	require.False(t, called)

	confirmDecision = "allow"
	allowRes, err := unrestricted.Call(ctx, map[string]any{"src": outsideFile, "dst": "1:/tmp/loot"})
	require.NoError(t, err)
	require.False(t, allowRes.IsError)
	require.True(t, called)
}

func TestMCPListAuditEnforcesSessionScopeAndLimitCap(t *testing.T) {
	origAudit := audit_repo.Audit()
	stub := &listAuditRepoStub{}
	audit_repo.RegisterAudit(stub)
	t.Cleanup(func() { audit_repo.RegisterAudit(origAudit) })

	auditTool := newMCPListAuditTool("mcp_session_xyz")
	require.True(t, auditTool.Audit)

	// Exceeding limit cap fails closed
	_, err := auditTool.Handler(context.Background(), map[string]any{"limit": float64(mcpMaxAuditListLimit + 1)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")

	// Normal call enforces Source="mcp" and SessionID="mcp_session_xyz"
	_, err = auditTool.Handler(context.Background(), map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	require.Len(t, stub.opts, 1)
	require.Equal(t, mcpAuditSource, stub.opts[0].Source)
	require.Equal(t, "mcp_session_xyz", stub.opts[0].SessionID)
	require.Equal(t, 10, stub.opts[0].Limit)
}
