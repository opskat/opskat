package command

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/agents/agent"
	cagotool "github.com/cago-frame/agents/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/mcp"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// shownPrompt is what the MCP client was asked: the message and the choices offered.
type shownPrompt struct {
	message string
	choices []string
}

// withPromptingClient runs fn inside a tool call of a real MCP server whose client
// answers every prompt with answer, and returns fn's result and the prompts shown.
func withPromptingClient[T any](t *testing.T, answer *mcpsdk.ElicitResult, lang string, fn func(ctx context.Context) T) (T, []shownPrompt) {
	t.Helper()
	var got T
	probe := &cagotool.RawTool{
		NameStr:   "probe",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			got = fn(ctx)
			return cagotool.TextResult("done"), nil
		},
	}
	srv := mcp.NewServer(mcp.ServerConfig{Tools: []agent.Tool{probe}, AuditWriter: &mcpAuditRecorder{}})

	var mu sync.Mutex
	var shown []shownPrompt
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			mu.Lock()
			defer mu.Unlock()
			shown = append(shown, shownPrompt{message: req.Params.Message, choices: offeredChoices(t, req.Params.RequestedSchema)})
			return answer, nil
		},
	})

	ctx, cancel := context.WithCancel(aictx.WithPolicyLang(context.Background(), lang))
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, serverT) }()
	session, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "probe", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	mu.Lock()
	defer mu.Unlock()
	return got, shown
}

func offeredChoices(t *testing.T, schema any) []string {
	t.Helper()
	props := schema.(map[string]any)["properties"].(map[string]any)
	options := props["decision"].(map[string]any)["oneOf"].([]any)
	out := make([]string, 0, len(options))
	for _, opt := range options {
		out = append(out, opt.(map[string]any)["const"].(string))
	}
	return out
}

func accept(decision string) *mcpsdk.ElicitResult {
	return &mcpsdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": decision}}
}

var sshExecItem = permission.ApprovalItem{Type: "exec", AssetID: 3, AssetName: "web-01", Command: "systemctl restart nginx"}

func TestMCPApproverOffersSessionAllowOnlyWhereAGrantCanBeSaved(t *testing.T) {
	a := mcpApprover{sessionID: "mcp_s"}

	resp, shown := withPromptingClient(t, accept("allowAll"), "en", func(ctx context.Context) permission.ApprovalResponse {
		return a.confirm(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{sshExecItem})
	})
	require.Equal(t, []string{"allow", "allowAll", "deny"}, shown[0].choices)
	require.Equal(t, "allowAll", resp.Decision, "the checker persists the session grant from allowAll")

	resp, shown = withPromptingClient(t, accept("allow"), "en", func(ctx context.Context) permission.ApprovalResponse {
		return a.confirm(ctx, permission.ApprovalKindOnce, []permission.ApprovalItem{sshExecItem})
	})
	require.Equal(t, []string{"allow", "deny"}, shown[0].choices)
	require.Equal(t, "allow", resp.Decision)
}

func TestMCPApproverDeniesAForgedChoiceThatWasNotOffered(t *testing.T) {
	// A well-behaved client validates the answer against the offered choices; on the
	// 2026-07-28 protocol the answer comes back inside the retried call, where nothing
	// but the server re-checks it.
	a := mcpApprover{sessionID: "mcp_s"}
	got := make(chan permission.ApprovalResponse, 1)
	probe := &cagotool.RawTool{
		NameStr:   "probe",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			got <- a.confirm(ctx, permission.ApprovalKindOnce, []permission.ApprovalItem{sshExecItem})
			return cagotool.TextResult("done"), nil
		},
	}
	srv := mcp.NewServer(mcp.ServerConfig{Tools: []agent.Tool{probe}, AuditWriter: &mcpAuditRecorder{}})
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "forger", Version: "1.0.0"}, &mcpsdk.ClientOptions{
		ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return nil, nil // never used: the retry below is driven by hand
		},
		MultiRoundTrip: &mcpsdk.MultiRoundTripOptions{Disabled: true},
	})
	ctx, cancel := context.WithCancel(context.Background())
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, serverT) }()
	session, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})

	first, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "probe", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, first.NeedsInput())
	forged := mcpsdk.InputResponseMap{}
	for id := range first.InputRequests {
		forged[id] = accept("allowAll")
	}
	_, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "probe", Arguments: map[string]any{}, InputResponses: forged, RequestState: first.RequestState,
	})
	require.NoError(t, err)
	require.Equal(t, "deny", (<-got).Decision, "a choice that was not offered must never authorize anything")
}

func TestMCPApproverTreatsAnythingButAnAcceptedChoiceAsDenial(t *testing.T) {
	a := mcpApprover{sessionID: "mcp_s"}
	for _, answer := range []*mcpsdk.ElicitResult{{Action: "decline"}, {Action: "cancel"}} {
		resp, _ := withPromptingClient(t, answer, "en", func(ctx context.Context) permission.ApprovalResponse {
			return a.confirm(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{sshExecItem})
		})
		require.Equal(t, "deny", resp.Decision, answer.Action)
	}
}

func TestMCPApproverPromptNamesTheAssetAndCommandInTheUserLanguage(t *testing.T) {
	a := mcpApprover{sessionID: "mcp_s"}
	_, shown := withPromptingClient(t, accept("deny"), "zh-cn", func(ctx context.Context) permission.ApprovalResponse {
		return a.confirm(ctx, permission.ApprovalKindBatch, []permission.ApprovalItem{
			sshExecItem,
			{Type: "exec", AssetID: 4, AssetName: "db-01", Command: "rm -rf /var/tmp/cache", Review: &aictx.ReviewInfo{Outcome: "rejected", Reason: "deletes files"}},
		})
	})
	lines := strings.Split(shown[0].message, "\n")
	require.Equal(t, "2 项操作待审批", lines[0])
	require.Equal(t, "web-01 [exec] systemctl restart nginx", lines[1], "clients show only the first lines; each operation must fit on one")
	require.Equal(t, "db-01 [exec] rm -rf /var/tmp/cache", lines[2])
	require.Equal(t, "模型审核：rejected deletes files", lines[3], "the model review's reason is what the human needs to see")
	require.Equal(t, []string{"allow", "deny"}, shown[0].choices, "a batch can only be allowed once or denied")
}

func TestMCPApproverGrantPersistsApprovedPatternsAsSessionGrants(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), int64(7)).Return(&asset_entity.Asset{ID: 7, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

	recGrant := &recordingGrantRepo{}
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(recGrant)
	t.Cleanup(func() { grant_repo.RegisterGrant(origGrant) })

	a := mcpApprover{sessionID: "mcp_s"}
	items := []permission.ApprovalItem{{Type: "grant", AssetID: 7, AssetName: "web-01", Command: "ls /tmp && pwd", Detail: "inspect"}}

	type grantResult struct {
		ok       bool
		patterns []string
	}
	denied, _ := withPromptingClient(t, &mcpsdk.ElicitResult{Action: "decline"}, "en", func(ctx context.Context) grantResult {
		ok, p := a.grant(ctx, items, "inspect")
		return grantResult{ok, p}
	})
	require.False(t, denied.ok)
	require.Empty(t, recGrant.saved)

	granted, shown := withPromptingClient(t, accept("allow"), "en", func(ctx context.Context) grantResult {
		ok, p := a.grant(ctx, items, "inspect")
		return grantResult{ok, p}
	})
	require.True(t, granted.ok)
	require.Equal(t, []string{"ls /tmp && pwd"}, granted.patterns)
	require.Equal(t, []string{"allow", "deny"}, shown[0].choices)
	require.Equal(t, "Grant for the rest of this session:\nweb-01: ls /tmp && pwd\nReason: inspect", shown[0].message)
	// Persisted on the asset's real approval face, split so each sub-command matches.
	require.Len(t, recGrant.saved, 2)
	require.Equal(t, "exec", recGrant.saved[0].ToolName)
	require.Equal(t, "ls /tmp", recGrant.saved[0].Command)
	require.Equal(t, "exec", recGrant.saved[1].ToolName)
	require.Equal(t, "pwd", recGrant.saved[1].Command)
}
