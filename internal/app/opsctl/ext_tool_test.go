package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
)

type extTestLang struct{}

func (extTestLang) Lang() string { return "en" }

type checkingExtExecutor struct {
	checkerPresent bool
}

func (e *checkingExtExecutor) ExecuteExtTool(ctx context.Context, _, _ string, _ []byte) ([]byte, error) {
	_, err := permission.RequireChecker(ctx)
	e.checkerPresent = err == nil
	return []byte(`{"ok":true}`), nil
}

func TestHandleExtToolExecInjectsPolicyChecker(t *testing.T) {
	executor := &checkingExtExecutor{}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	resp := o.handleExtToolExec(approval.ApprovalRequest{Extension: "echo", Tool: "run"})

	require.True(t, resp.Approved)
	require.True(t, executor.checkerPresent, "delegated extension execution must receive the desktop approval checker")
}
