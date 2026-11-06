package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cago-frame/agents/agent"
	cagotool "github.com/cago-frame/agents/tool"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/outputcap"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func execWithFake(t *testing.T, fakeType string, run permission.ExecFunc) (string, error) {
	t.Helper()
	m := setupUnified(t)
	permission.RegisterExecutor(fakeType, run, "doc", nil)
	t.Cleanup(func() { permission.UnregisterExecutor(fakeType) })
	asset := &asset_entity.Asset{ID: 43, Name: "cap-asset", Type: fakeType}
	m.EXPECT().FindByName(gomock.Any(), "43").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(43)).Return(asset, nil).AnyTimes()

	checker := permission.NewCommandPolicyChecker(func(context.Context, string, []permission.ApprovalItem) permission.ApprovalResponse {
		return permission.ApprovalResponse{Decision: "allow"}
	})
	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), fakeType)
	return callAIExecTool(t, ctx, map[string]any{"asset": "43", "command": "SELECT id, name FROM t"})
}

// callAIExecTool 经 AI 会话拿到的那个 exec 工具（Tools()）执行，返回它交给模型的文本。
func callAIExecTool(t *testing.T, ctx context.Context, args map[string]any) (string, error) {
	t.Helper()
	for _, tl := range Tools() {
		raw, ok := tl.(*cagotool.RawTool)
		if !ok || raw.NameStr != "exec" {
			continue
		}
		res, err := raw.Handler(ctx, args)
		if err != nil {
			return "", err
		}
		require.Len(t, res.Content, 1)
		text, ok := res.Content[0].(agent.TextBlock)
		require.True(t, ok)
		return text.Text, nil
	}
	t.Fatal("AI tool list has no exec tool")
	return "", nil
}

// execViaOpsctlTable 按 opsctl 的方式执行：从 AllToolDefs 取 exec handler，带着"已预检"调用
// （cmd/opsctl/command/handler.go 的 runHandler 与 batch 的 executeBatchHandler 都是这样）。
func execViaOpsctlTable(t *testing.T, fakeType string, run permission.ExecFunc) (string, error) {
	t.Helper()
	m := setupUnified(t)
	permission.RegisterExecutor(fakeType, run, "doc", nil)
	t.Cleanup(func() { permission.UnregisterExecutor(fakeType) })
	asset := &asset_entity.Asset{ID: 44, Name: "opsctl-asset", Type: fakeType}
	m.EXPECT().FindByName(gomock.Any(), "44").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(44)).Return(asset, nil).AnyTimes()
	for _, def := range AllToolDefs() {
		if def.Name == "exec" {
			ctx := aictx.WithAuditSource(permission.WithPreapproved(context.Background()), "opsctl")
			return def.Handler(ctx, map[string]any{"asset": "44", "command": "SELECT id, name FROM t"})
		}
	}
	t.Fatal("opsctl dispatch table has no exec handler")
	return "", nil
}

func bigLogOutput() string {
	var b strings.Builder
	for i := 1; i <= 50000; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	return b.String()
}

func TestAIExecTool_CapsLargeOutputKeepingHeadAndTail(t *testing.T) {
	raw := bigLogOutput()
	out, err := execWithFake(t, "test-cap-big", func(context.Context, *asset_entity.Asset, string, string) (string, error) {
		return raw, nil
	})
	require.NoError(t, err)
	t.Logf("SSH-like output: before=%d bytes, after=%d bytes", len(raw), len(out))
	assert.LessOrEqual(t, len(out), outputcap.MaxBytes)
	assert.True(t, strings.HasPrefix(out, "log line 1\n"))
	assert.True(t, strings.HasSuffix(out, "log line 50000\n"))
	assert.Contains(t, out, "omitted")
}

func TestAIExecTool_ReturnsSmallOutputVerbatim(t *testing.T) {
	out, err := execWithFake(t, "test-cap-small", func(context.Context, *asset_entity.Asset, string, string) (string, error) {
		return "hello\nworld\n", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "hello\nworld\n", out)
}

func TestAIExecTool_CapsSQLRowsAndStatesTotal(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Columns = []string{"id", "name"}
	fake.Rows = nil
	for i := 0; i < 100000; i++ {
		fake.Rows = append(fake.Rows, []any{i, "name-" + fmt.Sprint(i)})
	}
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{
		Config: &dbdriver.Config{Driver: fake.Desc.ID, Host: "h"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	uncapped, err := helper.ExecuteSQL(context.Background(), session, "SELECT id, name FROM t")
	require.NoError(t, err)

	out, err := execWithFake(t, "test-cap-sql", func(ctx context.Context, _ *asset_entity.Asset, sql, _ string) (string, error) {
		return helper.ExecuteSQL(ctx, session, sql)
	})
	require.NoError(t, err)
	t.Logf("100k-row SQL: before=%d bytes, after=%d bytes", len(uncapped), len(out))
	assert.LessOrEqual(t, len(out), outputcap.MaxBytes)
	assert.Contains(t, out, `"total_rows":100000`)
	assert.Contains(t, out, `"rows":[`)
}

// opsctl exec / batch 的非 SSH 资产同样经 AllToolDefs 的 exec handler 执行，它们的输出
// 必须保持完整：上限只属于 AI 会话的 exec 工具。
func TestOpsctlExec_OutputIsNotCapped(t *testing.T) {
	raw := bigLogOutput()
	out, err := execViaOpsctlTable(t, "test-opsctl-big", func(context.Context, *asset_entity.Asset, string, string) (string, error) {
		return raw, nil
	})
	require.NoError(t, err)
	assert.Equal(t, raw, out)
}

func TestOpsctlExec_SQLRowsAreNotCapped(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Columns = []string{"id"}
	fake.Rows = nil
	for i := 0; i < 1000; i++ {
		fake.Rows = append(fake.Rows, []any{i})
	}
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{
		Config: &dbdriver.Config{Driver: fake.Desc.ID, Host: "h"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	full, err := helper.ExecuteSQL(context.Background(), session, "SELECT id FROM t")
	require.NoError(t, err)
	out, err := execViaOpsctlTable(t, "test-opsctl-sql", func(ctx context.Context, _ *asset_entity.Asset, sql, _ string) (string, error) {
		return helper.ExecuteSQL(ctx, session, sql)
	})
	require.NoError(t, err)
	assert.Equal(t, full, out)
	assert.NotContains(t, out, "total_rows")
}
