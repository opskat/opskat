package command

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	cagotool "github.com/cago-frame/agents/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/mcp"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

func TestCmdMCPRejectsInvalidFlags(t *testing.T) {
	stderr := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--toolset=bogus"})
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr, "invalid --toolset")
}

func TestCmdMCPCoreToolsetAndIsolatedSession(t *testing.T) {
	origSessionDir := sessionDataDir
	sessionDir := t.TempDir()
	sessionDataDir = func() string { return sessionDir }
	t.Cleanup(func() { sessionDataDir = origSessionDir })
	// Seed a CLI active session and verify cmdMCP never overwrites or relies on it.
	require.NoError(t, writeActiveSession("cli-existing-session"))

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	origStdio := mcpStdio
	mcpStdio = func() mcpsdk.Transport { return serverT }
	t.Cleanup(func() { mcpStdio = origStdio })

	done := make(chan int, 1)
	go func() {
		done <- cmdMCP(context.Background(), []string{"--toolset=core"})
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)

	listRes, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make(map[string]*mcpsdk.Tool)
	for _, spec := range listRes.Tools {
		names[spec.Name] = spec
	}
	require.LessOrEqual(t, len(listRes.Tools), 10)
	require.Contains(t, names, "exec")
	require.Contains(t, names, "batch_exec")
	require.Contains(t, names, "cp")
	require.Contains(t, names, "request_permission")
	require.NotContains(t, names, "list_audit", "list_audit must stay out of core toolset")

	// The built-in tools' declared behavior reaches the client as annotations.
	require.NotNil(t, names["list_assets"].Annotations)
	require.True(t, names["list_assets"].Annotations.ReadOnlyHint)
	require.NotNil(t, names["exec"].Annotations)
	require.False(t, names["exec"].Annotations.ReadOnlyHint)
	require.True(t, *names["exec"].Annotations.DestructiveHint)

	// CLI active session file remained untouched
	require.Equal(t, "cli-existing-session", resolveSessionID())

	require.NoError(t, session.Close())
	select {
	case code := <-done:
		require.Equal(t, 0, code)
	case <-time.After(3 * time.Second):
		t.Fatal("cmdMCP did not exit after the client disconnected")
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
			"username": "root",
			"password": "SUPER_SECRET_PASSWORD_123",
		},
	}

	// Denied -> inner handler must NOT run, and approval detail must show safe non-secret host
	// while NEVER leaking secret values
	res, err := wrapped.Call(ctx, input)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.False(t, called)
	require.Equal(t, permission.ApprovalKindOnce, seenKind)
	require.Len(t, seenItems, 1)
	require.NotContains(t, seenItems[0].Detail, "SUPER_SECRET_PASSWORD_123")
	require.Contains(t, seenItems[0].Detail, `"host":"10.0.0.5"`)
	require.Contains(t, seenItems[0].Detail, `"username":"root"`)

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

	normDirs, err := helper.NormalizeAllowedLocalDirs([]string{allowedDir})
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

	// Exceeding limit cap fails closed
	_, err := auditTool.Call(context.Background(), map[string]any{"limit": float64(mcpMaxAuditListLimit + 1)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")

	// Normal call enforces Source="mcp" and SessionID="mcp_session_xyz"
	_, err = auditTool.Call(context.Background(), map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	require.Len(t, stub.opts, 1)
	require.Equal(t, mcp.AuditSource, stub.opts[0].Source)
	require.Equal(t, "mcp_session_xyz", stub.opts[0].SessionID)
	require.Equal(t, 10, stub.opts[0].Limit)
}

type mcpAuditRecorder struct {
	mu      sync.Mutex
	entries []audit.ToolCallInfo
}

func (r *mcpAuditRecorder) WriteToolCall(_ context.Context, info audit.ToolCallInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, info)
}

func TestGuardedPutAssetDeniedRedactsAuditAndShowsSafeHostInApproval(t *testing.T) {
	innerCalled := false
	innerPutAsset := &cagotool.RawTool{
		NameStr:   "put_asset",
		SchemaVal: agent.Schema{Type: "object"},
		IsSerial:  true,
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			innerCalled = true
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}}, nil
		},
	}

	var seenDetail string
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		if len(items) > 0 {
			seenDetail = items[0].Detail
		}
		return permission.ApprovalResponse{Decision: "deny"}
	})

	auditRec := &mcpAuditRecorder{}
	srv := mcp.NewServer(mcp.ServerConfig{
		Tools:       []agent.Tool{&guardedPutAssetTool{inner: innerPutAsset}},
		AuditWriter: auditRec,
		PrepareCtx: func(ctx context.Context) context.Context {
			return permission.WithPolicyChecker(ctx, checker)
		},
	})
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, serverT) }()
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "put_asset", Arguments: map[string]any{
		"name": "prod-ssh", "type": "ssh",
		"config": map[string]any{"host": "attacker.example", "port": 22, "username": "root", "password": "SUPER_SECRET_PASSWORD_123"},
	}})
	require.NoError(t, err)
	require.True(t, res.IsError)

	require.False(t, innerCalled)
	require.Contains(t, seenDetail, "attacker.example", "approval detail must show safe non-secret host to prevent blind approval")
	require.NotContains(t, seenDetail, "SUPER_SECRET_PASSWORD_123")

	auditRec.mu.Lock()
	defer auditRec.mu.Unlock()
	require.Len(t, auditRec.entries, 1)
	require.NotContains(t, auditRec.entries[0].ArgsJSON, "SUPER_SECRET_PASSWORD_123", "denied put_asset must never leak plaintext config secrets into audit_logs")
}

func TestGuardedCpToolBlocksGlobIntermediateSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Symlink creation on Windows may require elevated privileges; skip only if symlink creation fails.
		dir := t.TempDir()
		if err := os.Symlink(dir, filepath.Join(dir, "probe")); err != nil {
			t.Skipf("symlink creation unavailable on this Windows host: %v", err)
		}
	}

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
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.key"), []byte("top-secret"), 0o600))
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(allowedDir, "sub1")))

	normDirs, err := helper.NormalizeAllowedLocalDirs([]string{allowedDir})
	require.NoError(t, err)

	called := false
	innerCp := &cagotool.RawTool{
		NameStr: "cp",
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			called = true
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "copied"}}}, nil
		},
	}
	whitelisted := &guardedCpTool{inner: innerCp, allowedLocalDirs: normDirs}
	globEscape := filepath.Join(allowedDir, "sub*", "secret.key")
	_, err = whitelisted.Call(context.Background(), map[string]any{"src": globEscape, "dst": "1:/tmp/exfil"})
	require.Error(t, err, "glob through intermediate symlink must be blocked by --allow-local-dir")
	require.False(t, called)
}

type recordingGrantRepo struct {
	grant_repo.GrantRepo
	saved []*grant_entity.GrantItem
}

func (r *recordingGrantRepo) GetSession(_ context.Context, id string) (*grant_entity.GrantSession, error) {
	return &grant_entity.GrantSession{ID: id, Status: grant_entity.GrantStatusApproved}, nil
}

func (r *recordingGrantRepo) CreateItems(_ context.Context, items []*grant_entity.GrantItem) error {
	r.saved = append(r.saved, items...)
	return nil
}
