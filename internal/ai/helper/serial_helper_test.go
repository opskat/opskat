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

func TestHandleRunSerialCommandSuccess(t *testing.T) {
	sess := &fakeSerialSession{output: "version\r\nOK\r\n"}
	ctx := WithSerialManager(context.Background(), &fakeSerialManager{session: sess, ok: true})

	result, err := HandleRunSerialCommand(ctx, map[string]any{
		"asset_id": int64(7),
		"command":  "display version",
	})

	require.NoError(t, err)
	assert.Equal(t, "version\r\nOK\r\n", result)
	assert.Equal(t, 1, sess.calls)
	assert.Equal(t, "display version", sess.lastCommand)
}

func TestHandleRunSerialCommandRequiresActiveSession(t *testing.T) {
	ctx := WithSerialManager(context.Background(), &fakeSerialManager{ok: false})

	_, err := HandleRunSerialCommand(ctx, map[string]any{
		"asset_id": int64(7),
		"command":  "display version",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active serial session")
}

func TestHandleRunSerialCommandRespectsSerialPolicy(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Type: asset_entity.AssetTypeSerial,
		CmdPolicy: mustJSON(asset_entity.CommandPolicy{
			DenyList: []string{"reload *"},
		}),
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	sess := &fakeSerialSession{output: "should not execute"}
	ctx = permission.WithPolicyChecker(ctx, permission.NewCommandPolicyChecker(nil))
	ctx = WithSerialManager(ctx, &fakeSerialManager{session: sess, ok: true})

	result, err := HandleRunSerialCommand(ctx, map[string]any{
		"asset_id": int64(1),
		"command":  "reload now",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Equal(t, 0, sess.calls)
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
