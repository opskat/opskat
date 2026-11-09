package command

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// reviewedGenericHTTPExec 把 echo-prod（setupGenericHTTPExec）设成 Autopilot、审核一律通过，
// 返回记录送审文本的审核端口替身。
func reviewedGenericHTTPExec(t *testing.T) (context.Context, *fakeReviewer) {
	t.Helper()
	ctx, _ := setupGenericHTTPExec(t, "s3cret")
	isolateApprovers(t)
	assets, err := asset_repo.Asset().FindByName(ctx, "echo-prod")
	require.NoError(t, err)
	require.Len(t, assets, 1)
	assets[0].PermissionMode = policyent.PermissionModeAutopilot
	require.NoError(t, asset_repo.Asset().Update(ctx, assets[0]))
	r := &fakeReviewer{result: aictx.ReviewInfo{Outcome: aictx.ReviewPass}}
	useReviewer(t, r)
	origStdin := execStdinFn
	execStdinFn = func(time.Duration) (io.Reader, bool) { return nil, false }
	t.Cleanup(func() { execStdinFn = origStdin })
	return ctx, r
}

// 模型审核看的是调用方写的整条请求：策略匹配对象 `POST /api/items` 里没有请求体与 query，
// 只按它送审等于在看不到写入内容的情况下放行。
func TestCmdExec_GenericHTTPReviewSeesTheRequestAsWritten(t *testing.T) {
	ctx, r := reviewedGenericHTTPExec(t)

	var code int
	_, stderr := captureExecStdio(t, "", func() {
		code = cmdExec(ctx, buildHandlerMap(), []string{"echo-prod", "--", "POST", "/api/items?dry=1", "-d", `{"role":"Admin"}`}, "")
	})

	require.Equal(t, 0, code, stderr)
	require.Len(t, r.commands, 1)
	assert.Contains(t, r.commands[0], "/api/items?dry=1")
	assert.Contains(t, r.commands[0], `{"role":"Admin"}`)
}

func TestCmdBatch_GenericHTTPReviewSeesTheRequestAsWritten(t *testing.T) {
	ctx, r := reviewedGenericHTTPExec(t)

	runBatchCapturingStdout(t, ctx, map[string]tool.ToolHandlerFunc{}, []string{`echo-prod:POST /api/items?dry=1 -d '{"role":"Admin"}'`}, "")

	require.Len(t, r.commands, 1)
	assert.Equal(t, `POST /api/items?dry=1 -d '{"role":"Admin"}'`, r.commands[0])
}
