package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

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
	return handleExec(ctx, map[string]any{"asset": "43", "command": "SELECT id, name FROM t"})
}

func TestHandleExec_CapsLargeOutputKeepingHeadAndTail(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 50000; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	raw := b.String()
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

func TestHandleExec_ReturnsSmallOutputVerbatim(t *testing.T) {
	out, err := execWithFake(t, "test-cap-small", func(context.Context, *asset_entity.Asset, string, string) (string, error) {
		return "hello\nworld\n", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "hello\nworld\n", out)
}

func TestHandleExec_CapsSQLRowsAndStatesTotal(t *testing.T) {
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
