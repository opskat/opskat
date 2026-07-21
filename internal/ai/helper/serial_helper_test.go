package helper

import (
	"context"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/serial_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type fakeSerialManager struct {
	session serial_svc.CommandSession
	ok      bool
}

func (m *fakeSerialManager) GetSessionByAssetID(_ int64) (serial_svc.CommandSession, bool) {
	return m.session, m.ok
}

type fakeSerialSession struct {
	output      string
	err         error
	calls       int
	lastCommand string
}

func (s *fakeSerialSession) ExecCommand(command string, _ time.Duration, _ time.Duration) (string, error) {
	s.calls++
	s.lastCommand = command
	return s.output, s.err
}

// TestNoActiveSerialSession_SameErrorFromBothPaths locks errNoActiveSerialSession's
// reason for existing: the precheck registered for the unified exec
// (PrecheckSerialSession) and the pure executor (ExecSerialOnAsset) must report the
// same sentence. They are reached in that order for one command, and a user who sees
// two different wordings for one condition cannot tell they are the same problem.
//
// This replaces TestHandleRunSerialCommandRequiresActiveSession, which asserted the
// same condition on the deleted run_serial_command tool.
func TestNoActiveSerialSession_SameErrorFromBothPaths(t *testing.T) {
	ctx := WithSerialManager(context.Background(), &fakeSerialManager{ok: false})
	asset := &asset_entity.Asset{ID: 7, Type: asset_entity.AssetTypeSerial}

	precheckErr := PrecheckSerialSession(ctx, asset)
	require.Error(t, precheckErr)
	assert.Contains(t, precheckErr.Error(), "no active serial session")

	_, execErr := ExecSerialOnAsset(ctx, asset, "display version", "")
	require.Error(t, execErr)
	assert.Equal(t, precheckErr.Error(), execErr.Error())
}

// TestExecSerialOnAsset_SendsCommandVerbatim keeps the coverage the deleted
// TestHandleRunSerialCommandSuccess had over the happy path: the command reaches the
// session unmodified and its output is returned as-is.
func TestExecSerialOnAsset_SendsCommandVerbatim(t *testing.T) {
	sess := &fakeSerialSession{output: "version\r\nOK\r\n"}
	ctx := WithSerialManager(context.Background(), &fakeSerialManager{session: sess, ok: true})

	result, err := ExecSerialOnAsset(ctx, &asset_entity.Asset{ID: 7}, "display version", "")

	require.NoError(t, err)
	assert.Equal(t, "version\r\nOK\r\n", result)
	assert.Equal(t, 1, sess.calls)
	assert.Equal(t, "display version", sess.lastCommand)
}

// TestExecSerialOnAsset_IgnoresPolicyChecker proves the pure-exec body registered
// as the unified exec's ExecFunc does NOT consult the policy checker in ctx — the
// check is the unified exec tool's job, once, before dispatch (Task 6). Under the
// identical deny policy that the (now deleted) run_serial_command tool used to block
// before ever touching the session, calling ExecSerialOnAsset directly must still
// execute. If a permission check were
// ever reintroduced into this function, the command would be blocked here too and
// this test would fail — that's exactly the double-approval-dialog regression the
// split guards against.
func TestExecSerialOnAsset_IgnoresPolicyChecker(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Type: asset_entity.AssetTypeSerial,
		CmdPolicy: mustJSON(asset_entity.CommandPolicy{
			DenyList: []string{"reload *"},
		}),
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	sess := &fakeSerialSession{output: "should execute"}
	ctx = permission.WithPolicyChecker(ctx, permission.NewCommandPolicyChecker(nil))
	ctx = WithSerialManager(ctx, &fakeSerialManager{session: sess, ok: true})

	result, err := ExecSerialOnAsset(ctx, asset, "reload now", "")

	require.NoError(t, err)
	assert.Equal(t, "should execute", result)
	assert.Equal(t, 1, sess.calls)
}

func TestCommandPolicyCheckerSerialApprovalType(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Name: "console-1",
		Type: asset_entity.AssetTypeSerial,
		CmdPolicy: mustJSON(asset_entity.CommandPolicy{
			AllowList: []string{"show *"},
		}),
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	var approvalType string
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		if len(items) > 0 {
			approvalType = items[0].Type
		}

		return permission.ApprovalResponse{Decision: "allow"}
	})

	result := checker.CheckForAsset(ctx, 1, asset_entity.AssetTypeSerial, "reload now")
	assert.Equal(t, aictx.Allow, result.Decision)
	assert.Equal(t, aictx.SourceUserAllow, result.DecisionSource)
	assert.Equal(t, "serial", approvalType)
}
