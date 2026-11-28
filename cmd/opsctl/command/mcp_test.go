package command

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/mcp"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo/mock_custom_type_repo"
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

	customTypes := mock_custom_type_repo.NewMockCustomTypeRepo(gomock.NewController(t))
	customTypes.EXPECT().List(gomock.Any()).Return([]*custom_type_entity.CustomType{{Slug: "echo", Name: "Echo service"}}, nil).AnyTimes()
	origCustomTypes := custom_type_repo.CustomType()
	custom_type_repo.RegisterCustomType(customTypes)
	t.Cleanup(func() { custom_type_repo.RegisterCustomType(origCustomTypes) })

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

	// The client gets the built-in AI's tool guidance, with this machine's types, but
	// nothing about the desktop AI's own local_* tools, which this server does not have.
	instructions := session.InitializeResult().Instructions
	require.Contains(t, instructions, "- echo: Echo service")
	require.Contains(t, instructions, "help(asset)")
	require.NotContains(t, instructions, "local_bash")

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

// The full toolset is the built-in AI's tools, except that a stored secret's plaintext
// is never handed to an external agent.
func TestMCPFullToolsetNeverServesSecretValues(t *testing.T) {
	served := mcpToolNames(mcp.ToolsetFull, nil)
	require.Contains(t, served, "put_asset")
	require.Contains(t, served, "list_audit")
	require.NotContains(t, served, "get_asset_secret")
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

// Through opsctl mcp every put_asset asks the human, who sees the non-secret host before
// anything is written; a denial leaves no secret in audit.
func TestMCPPutAssetAsksShowingTheHostAndKeepsSecretsOutOfAudit(t *testing.T) {
	var seenMessage string
	auditRec := &mcpAuditRecorder{}
	srv := mcp.NewServer(mcp.ServerConfig{
		Tools:       mcpTools(mcp.ToolsetFull, "s"),
		AuditWriter: auditRec,
		PrepareCtx:  mcpCallContext(mcpApprover{sessionID: "s"}, nil),
	})
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, serverT) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			seenMessage = req.Params.Message
			return &mcpsdk.ElicitResult{Action: "decline"}, nil
		},
	})
	session, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("mcp server stopped with %v", err)
		}
	})

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "put_asset", Arguments: map[string]any{
		"name": "prod-ssh", "type": "ssh",
		"config": map[string]any{"host": "attacker.example", "port": 22, "username": "root", "password": "SUPER_SECRET_PASSWORD_123"},
	}})
	require.NoError(t, err)
	require.Contains(t, res.Content[0].(*mcpsdk.TextContent).Text, "USER DENIED")
	require.Contains(t, seenMessage, "attacker.example", "the human must see where the credentials would go")
	require.NotContains(t, seenMessage, "SUPER_SECRET_PASSWORD_123")

	auditRec.mu.Lock()
	defer auditRec.mu.Unlock()
	require.Len(t, auditRec.entries, 1)
	require.NotContains(t, auditRec.entries[0].ArgsJSON, "SUPER_SECRET_PASSWORD_123")
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
