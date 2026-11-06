package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// recordingReviewer 是审核端口的替身：一律通过，并记下送审的命令文本。
type recordingReviewer struct{ commands []string }

func (r *recordingReviewer) ReviewCommands(_ context.Context, reqs []permission.ReviewRequest) []aictx.ReviewInfo {
	out := make([]aictx.ReviewInfo, len(reqs))
	for i, req := range reqs {
		r.commands = append(r.commands, req.Command)
		out[i] = aictx.ReviewInfo{Outcome: aictx.ReviewPass}
	}
	return out
}

// reviewedMetricsAssets 在 setupGenericBatch 的 metrics-a / metrics-b 上开 Autopilot，
// 返回已过 help 门禁、带权限检查器的 ctx 与记录送审文本的审核端口替身。
func reviewedMetricsAssets(t *testing.T) (context.Context, *recordingReviewer) {
	t.Helper()
	setupGenericBatch(t)
	for _, name := range []string{"metrics-a", "metrics-b"} {
		assets, err := asset_repo.Asset().FindByName(context.Background(), name)
		require.NoError(t, err)
		require.Len(t, assets, 1)
		assets[0].PermissionMode = policyent.PermissionModeAutopilot
		require.NoError(t, asset_repo.Asset().Update(context.Background(), assets[0]))
	}
	r := &recordingReviewer{}
	useReviewer(t, r)
	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, permission.NewCommandPolicyChecker(func(context.Context, string, []permission.ApprovalItem) permission.ApprovalResponse {
		t.Fatal("Autopilot with a passing review must not ask a person")
		return permission.ApprovalResponse{}
	}))
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), "metrics")
	return ctx, r
}

// 模型审核看的是模型写的整条请求，而不只是策略匹配对象 `POST /api/x`：请求体与 query
// 决定了这次写操作会做什么。
func TestHandleExec_GenericHTTPReviewSeesTheRequestAsWritten(t *testing.T) {
	ctx, r := reviewedMetricsAssets(t)
	command := `POST /api/x?dry=1 -d '{"role":"Admin"}'`

	_, err := handleExec(ctx, map[string]any{"asset": "metrics-a", "command": command})

	require.NoError(t, err)
	assert.Equal(t, []string{command}, r.commands)
}

func TestHandleBatchCommand_GenericHTTPReviewSeesTheRequestAsWritten(t *testing.T) {
	ctx, r := reviewedMetricsAssets(t)

	_, err := handleBatchCommand(ctx, map[string]any{
		"commands": `[{"asset":"metrics-a","command":"POST /api/x -d '{\"a\":1}'"},{"asset":"metrics-b","command":"DELETE /api/y?force=1"}]`,
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{`POST /api/x -d '{"a":1}'`, "DELETE /api/y?force=1"}, r.commands)
}
