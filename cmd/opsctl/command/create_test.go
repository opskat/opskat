package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/asset_put_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/mock/gomock"
)

type fakePreparedAssetCreate struct {
	approval  map[string]any
	audit     map[string]any
	result    *asset_put_svc.Result
	commitErr error
	onCommit  func()
}

func (f *fakePreparedAssetCreate) SafeApprovalDetail() map[string]any { return f.approval }
func (f *fakePreparedAssetCreate) SafeAuditArgsForResult(result *asset_put_svc.Result) map[string]any {
	if f.audit != nil {
		return f.audit
	}
	out := map[string]any{}
	for key, value := range f.approval {
		out[key] = value
	}
	if result != nil {
		out["id"] = result.ID
		if result.Authentication != nil {
			out["authentication"] = *result.Authentication
		}
	}
	return out
}
func (f *fakePreparedAssetCreate) Commit(context.Context) (*asset_put_svc.Result, error) {
	if f.onCommit != nil {
		f.onCommit()
	}
	return f.result, f.commitErr
}

func validCreateConfig(assetType string) map[string]any {
	fixtures := map[string]map[string]any{
		asset_entity.AssetTypeSSH:      {"host": "ssh.internal", "username": "root"},
		asset_entity.AssetTypeDatabase: {"driver": "sqlite", "path": "/tmp/test.db"},
		asset_entity.AssetTypeRedis:    {"host": "redis.internal", "username": "default"},
		asset_entity.AssetTypeMongoDB:  {"host": "mongo.internal", "username": "app"},
		asset_entity.AssetTypeKafka:    {"brokers": []any{"kafka.internal:9092"}},
		asset_entity.AssetTypeK8s:      {"kubeconfig": "apiVersion: v1"},
		asset_entity.AssetTypeSerial:   {"port_path": "/dev/ttyUSB0", "baud_rate": int64(115200)},
		asset_entity.AssetTypeEtcd:     {"endpoints": []any{"etcd.internal:2379"}},
		asset_entity.AssetTypeLocal:    {},
		asset_entity.AssetTypeVNC:      {"host": "vnc.internal", "username": "operator"},
		asset_entity.AssetTypeRDP:      {"host": "rdp.internal", "username": "administrator"},
		asset_entity.AssetTypeOSS:      {"provider": "s3", "endpoint": "s3.internal", "access_key_id": "AKIA"},
	}
	return fixtures[assetType]
}

func createArgs(t *testing.T, assetType string, config map[string]any) []string {
	t.Helper()
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	return []string{"--type", assetType, "--name", "asset-" + assetType, "--config", string(encoded)}
}

func preserveCreateSeams(t *testing.T) {
	t.Helper()
	oldPrepare := prepareAssetPut
	oldApproval := requireCreateApproval
	oldUpdateApproval := requireUpdateApproval
	oldNotify := notifyAssetChanged
	t.Cleanup(func() {
		prepareAssetPut = oldPrepare
		requireCreateApproval = oldApproval
		requireUpdateApproval = oldUpdateApproval
		notifyAssetChanged = oldNotify
	})
}

// registerMockAssetRepo swaps in a mock AssetRepo whose List returns assets, restoring the
// original registration on cleanup. updateAsset resolves its target asset ref through
// resolveAsset, which for a non-numeric ref goes through AssetRepo.List.
func registerMockAssetRepo(t *testing.T, ctrl *gomock.Controller, assets []*asset_entity.Asset) *mock_asset_repo.MockAssetRepo {
	t.Helper()
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return(assets, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})
	return mockAsset
}

func TestCreateAssetParserFeedsRealSharedPrepareForEveryRegisteredBuiltin(t *testing.T) {
	for _, handler := range assettype.All() {
		t.Run(handler.Type(), func(t *testing.T) {
			config := validCreateConfig(handler.Type())
			if config == nil {
				t.Fatalf("missing parser fixture for registered built-in type %q", handler.Type())
			}
			var stderr bytes.Buffer
			request, err := parseAssetCreate(context.Background(), createArgs(t, handler.Type(), config), assetCreateParserDeps{
				stderr: &stderr, readFile: func(string) ([]byte, error) { return nil, errors.New("unexpected read") },
				resolveAssetID: func(context.Context, string) (int64, error) { return 0, errors.New("unexpected resolve") },
			})
			require.NoError(t, err, stderr.String())
			prepared, err := asset_put_svc.Prepare(context.Background(), asset_put_svc.Request{Asset: request.asset, Config: request.config})
			require.NoError(t, err)
			assert.Equal(t, handler.Type(), prepared.SafeApprovalDetail()["type"])
		})
	}
}

func TestCreateAssetEveryRegisteredBuiltinReachesSharedPrepareWithoutHardcodedTypeList(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	opsctlAuditWriter = &mockAuditWriter{}
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	preparedTypes := map[string]bool{}
	prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		preparedTypes[request.Asset.Type] = true
		return &fakePreparedAssetCreate{
			approval: map[string]any{"name": request.Asset.Name, "type": request.Asset.Type},
			result:   &asset_put_svc.Result{ID: 77},
		}, nil
	}
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyAssetChanged = func() {}

	for _, handler := range assettype.All() {
		config := validCreateConfig(handler.Type())
		if config == nil {
			t.Fatalf("missing parser fixture for registered built-in type %q", handler.Type())
		}
		var stdout, stderr bytes.Buffer
		code := createAsset(context.Background(), createArgs(t, handler.Type(), config), "session", commandIO{
			stdout: &stdout, stderr: &stderr,
		})
		assert.Equal(t, 0, code, "type=%s stderr=%s", handler.Type(), stderr.String())
		assert.Contains(t, stdout.String(), `"id": 77`)
	}
	for _, handler := range assettype.All() {
		assert.True(t, preparedTypes[handler.Type()], "registered type %q never reached asset_put_svc.Prepare", handler.Type())
	}
}

func TestCreateAssetLegacyConvenienceFlagsPreserveHandlerOwnedDefaultPorts(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want int
	}{
		{name: "SSH", args: []string{"--name", "box", "--host", "ssh.internal", "--username", "root"}, want: 22},
		{name: "PostgreSQL", args: []string{"--type", "database", "--name", "pg", "--driver", "postgresql", "--host", "pg.internal", "--username", "reader"}, want: 5432},
		{name: "Redis", args: []string{"--type", "redis", "--name", "cache", "--host", "redis.internal", "--username", "default"}, want: 6379},
		{name: "MongoDB", args: []string{"--type", "mongodb", "--name", "mongo", "--host", "mongo.internal", "--username", "app"}, want: 27017},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, _, err := parseAssetCreateForTest(t, tt.args, nil, nil)
			require.NoError(t, err)
			prepared, err := asset_put_svc.Prepare(context.Background(), asset_put_svc.Request{Asset: request.asset, Config: request.config})
			require.NoError(t, err)
			approvalConfig := prepared.SafeApprovalDetail()["config"].(map[string]any)
			assert.Equal(t, tt.want, approvalConfig["port"])
		})
	}
}

func TestCreateAssetPrepareBeforeApprovalCommitAfterApprovalAndSafeMetadataOnly(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	opsctlAuditWriter = &mockAuditWriter{}
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	sequence := []string{}
	prepared := &fakePreparedAssetCreate{
		approval: map[string]any{"name": "cache", "type": "redis", "config": map[string]any{"host": "redis.internal"}},
		result:   &asset_put_svc.Result{ID: 42, Authentication: &asset_put_svc.AuthenticationRef{Type: "password", Ref: 9}},
		onCommit: func() { sequence = append(sequence, "commit") },
	}
	prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		sequence = append(sequence, "prepare")
		assert.Equal(t, "top-secret", request.Config["password"])
		return prepared, nil
	}
	var approvalReq approval.ApprovalRequest
	requireCreateApproval = func(_ context.Context, request approval.ApprovalRequest) (ApprovalResult, error) {
		sequence = append(sequence, "approval")
		approvalReq = request
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notified := false
	notifyAssetChanged = func() { notified = true }

	var stdout, stderr bytes.Buffer
	code := createAsset(context.Background(), []string{
		"--type", "redis", "--name", "cache", "--config", `{"host":"redis.internal","username":"default"}`, "--password", "top-secret",
	}, "session", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())
	assert.Equal(t, []string{"prepare", "approval", "commit"}, sequence)
	assert.NotContains(t, approvalReq.Detail, "top-secret")
	assert.NotContains(t, approvalReq.Detail, "password")
	assert.Contains(t, approvalReq.Detail, "cache")
	assert.Contains(t, approvalReq.Detail, "redis")
	assert.True(t, notified)
	assert.Contains(t, stdout.String(), `"id": 42`)
	assert.Contains(t, stdout.String(), `"authentication"`)
	assert.NotContains(t, stdout.String(), "top-secret")
}

func TestCreateAssetInvalidReferenceMismatchAndDenialNeverCommit(t *testing.T) {
	preserveCreateSeams(t)
	notifyCalls := 0
	notifyAssetChanged = func() { notifyCalls++ }

	t.Run("prepare validation", func(t *testing.T) {
		prepareAssetPut = func(context.Context, asset_put_svc.Request) (preparedAssetCreate, error) {
			return nil, errors.New("referenced credential type mismatch")
		}
		approvalCalls := 0
		requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
			approvalCalls++
			return ApprovalResult{}, nil
		}
		var stdout, stderr bytes.Buffer
		code := createAsset(context.Background(), createArgs(t, "redis", validCreateConfig("redis")), "session", commandIO{stdout: &stdout, stderr: &stderr})
		assert.Equal(t, 1, code)
		assert.Zero(t, approvalCalls)
		assert.Contains(t, stderr.String(), "type mismatch")
	})

	t.Run("denied", func(t *testing.T) {
		commitCalls := 0
		prepareAssetPut = func(context.Context, asset_put_svc.Request) (preparedAssetCreate, error) {
			return &fakePreparedAssetCreate{
				approval: map[string]any{"name": "cache", "type": "redis"},
				result:   &asset_put_svc.Result{ID: 1}, onCommit: func() { commitCalls++ },
			}, nil
		}
		requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
			return ApprovalResult{}, errors.New("operation denied")
		}
		var stdout, stderr bytes.Buffer
		code := createAsset(context.Background(), createArgs(t, "redis", validCreateConfig("redis")), "session", commandIO{stdout: &stdout, stderr: &stderr})
		assert.Equal(t, 1, code)
		assert.Zero(t, commitCalls)
		assert.Contains(t, stderr.String(), "operation denied")
	})
	assert.Zero(t, notifyCalls)
}

func TestCreateAssetCommitFailureAuditsSafeErrorAndDoesNotNotify(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	prepareAssetPut = func(context.Context, asset_put_svc.Request) (preparedAssetCreate, error) {
		return &fakePreparedAssetCreate{
			approval:  map[string]any{"name": "cache", "type": "redis", "config": map[string]any{"host": "redis.internal"}},
			commitErr: errors.New("asset write failed"),
		}, nil
	}
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyCalls := 0
	notifyAssetChanged = func() { notifyCalls++ }
	writer := &mockAuditWriter{}
	opsctlAuditWriter = writer

	var stdout, stderr bytes.Buffer
	code := createAsset(context.Background(), []string{
		"--type", "redis", "--name", "cache", "--config", `{"host":"redis.internal","username":"default"}`, "--password", "failure-top-secret",
	}, "session", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 1, code)
	require.Len(t, writer.calls, 1)
	call := writer.lastCall()
	assert.Error(t, call.Error)
	assert.NotContains(t, call.ArgsJSON, "failure-top-secret")
	assert.NotContains(t, call.Error.Error(), "failure-top-secret")
	assert.Zero(t, notifyCalls)
	assert.Empty(t, stdout.String())
}

func TestCreateAssetNilCommitResultFailsClosedAndIsAudited(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	writer := &mockAuditWriter{}
	opsctlAuditWriter = writer
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	prepareAssetPut = func(context.Context, asset_put_svc.Request) (preparedAssetCreate, error) {
		return &fakePreparedAssetCreate{
			approval: map[string]any{"name": "cache", "type": "redis", "config": map[string]any{"host": "redis.internal"}},
		}, nil
	}
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyCalls := 0
	notifyAssetChanged = func() { notifyCalls++ }

	var stdout, stderr bytes.Buffer
	assert.NotPanics(t, func() {
		code := createAsset(context.Background(), createArgs(t, "redis", validCreateConfig("redis")), "session", commandIO{
			stdout: &stdout, stderr: &stderr,
		})
		assert.Equal(t, 1, code)
	})

	require.Len(t, writer.calls, 1)
	call := writer.lastCall()
	require.Error(t, call.Error)
	assert.Contains(t, call.Error.Error(), "no result")
	assert.Contains(t, stderr.String(), "no result")
	assert.Empty(t, stdout.String())
	assert.Zero(t, notifyCalls)
}

func TestCreateAssetOutputWriteFailureReturnsNonzeroAndReportsError(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	opsctlAuditWriter = &mockAuditWriter{}
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	prepareAssetPut = func(context.Context, asset_put_svc.Request) (preparedAssetCreate, error) {
		return &fakePreparedAssetCreate{
			approval: map[string]any{"name": "cache", "type": "redis"},
			result:   &asset_put_svc.Result{ID: 8},
		}, nil
	}
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyAssetChanged = func() {}

	var stderr bytes.Buffer
	code := createAsset(context.Background(), createArgs(t, "redis", validCreateConfig("redis")), "session", commandIO{
		stdout: failingWriter{err: errors.New("stdout closed")}, stderr: &stderr,
	})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "write asset result")
	assert.Contains(t, stderr.String(), "stdout closed")
}

func TestCreateAssetCommitAuditUsesOnlySafeArgsAndOutput(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	prepareAssetPut = func(context.Context, asset_put_svc.Request) (preparedAssetCreate, error) {
		return &fakePreparedAssetCreate{
			approval: map[string]any{"name": "cache", "type": "redis", "config": map[string]any{"host": "redis.internal"}},
			result:   &asset_put_svc.Result{ID: 8, Authentication: &asset_put_svc.AuthenticationRef{Type: "password", Ref: 5}},
		}, nil
	}
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyAssetChanged = func() {}
	writer := &mockAuditWriter{}
	opsctlAuditWriter = writer

	var stdout, stderr bytes.Buffer
	code := createAsset(context.Background(), []string{
		"--type", "redis", "--name", "cache", "--config", `{"host":"redis.internal","username":"default"}`, "--password", "audit-top-secret",
	}, "session", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())
	require.Len(t, writer.calls, 1)
	call := writer.lastCall()
	assert.Equal(t, "put_asset", call.ToolName)
	assert.NotContains(t, call.ArgsJSON, "audit-top-secret")
	assert.NotContains(t, call.Result, "audit-top-secret")
	assert.Contains(t, call.Result, `"id":8`)
	assert.Contains(t, call.Result, `"ref":5`)
}

// realProjectionPrepared 复用真实的 asset_put_svc.Prepared 投影方法（SafeApprovalDetail /
// SafeAuditArgsForResult），只用假 Commit 结果避免真实物化依赖 db —— 证明 opsctl create
// 的 Audit 走的就是 producer 自己的投影，而不是一份独立复制。
type realProjectionPrepared struct {
	*asset_put_svc.Prepared
	result *asset_put_svc.Result
}

func (p *realProjectionPrepared) Commit(context.Context) (*asset_put_svc.Result, error) {
	return p.result, nil
}

func TestCreateAssetAuditReusesRealProducerProjection(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	writer := &mockAuditWriter{}
	opsctlAuditWriter = writer
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyAssetChanged = func() {}

	prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		prepared, err := asset_put_svc.Prepare(context.Background(), request)
		if err != nil {
			return nil, err
		}
		return &realProjectionPrepared{Prepared: prepared, result: &asset_put_svc.Result{
			ID: 7, Authentication: &asset_put_svc.AuthenticationRef{Type: "password", Ref: 3},
		}}, nil
	}

	var stdout, stderr bytes.Buffer
	code := createAsset(context.Background(), []string{
		"--type", "redis", "--name", "cache", "--config", `{"host":"redis.internal","username":"default"}`, "--password", "opsctl-producer-secret",
	}, "session", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())

	call := writer.lastCall()
	assert.Equal(t, "put_asset", call.ToolName)
	assert.NotContains(t, call.ArgsJSON, "opsctl-producer-secret")
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(call.ArgsJSON), &args))
	assert.Equal(t, "cache", args["name"])
	assert.Equal(t, "redis", args["type"])
	config, ok := args["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "redis.internal", config["host"])
	_, hasPassword := config["password"]
	assert.False(t, hasPassword, "write-only password must be absent from the opsctl create audit")
	auth, ok := args["authentication"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "password", auth["type"])
	assert.Equal(t, float64(3), auth["ref"])
}

// TestCreateAssetCompositeConfigOmittedFromAuditViaRealPrepare 通过真实 asset_put_svc
// Prepare 的 producer 投影证明 opsctl create 审计不携带 allowlist 键下的嵌套 secret：可选
// 审批字段的复合值被 approvalView 整体省略，合法扁平字符串数组归一化保留。
func TestCreateAssetCompositeConfigOmittedFromAuditViaRealPrepare(t *testing.T) {
	preserveCreateSeams(t)
	oldWriter := opsctlAuditWriter
	writer := &mockAuditWriter{}
	opsctlAuditWriter = writer
	t.Cleanup(func() { opsctlAuditWriter = oldWriter })
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyAssetChanged = func() {}

	prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		prepared, err := asset_put_svc.Prepare(context.Background(), request)
		if err != nil {
			return nil, err
		}
		return &realProjectionPrepared{Prepared: prepared, result: &asset_put_svc.Result{ID: 7}}, nil
	}

	// #nosec G101 -- 嵌套 secret 是故意用于证明 opsctl 审计不携带复合值藏匿秘密的夹具。
	secret := "opsctl-nested-secret-must-not-leak"
	var stdout, stderr bytes.Buffer
	code := createAsset(context.Background(), []string{
		"--type", "ssh", "--name", "box",
		"--config", `{"host":"10.0.0.1","username":"root","auth_type":{"password":"` + secret + `"}}`,
	}, "session", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())

	call := writer.lastCall()
	require.NotNil(t, call)
	assert.Equal(t, "put_asset", call.ToolName)
	assert.NotContains(t, call.ArgsJSON, secret)
	assert.NotContains(t, call.Result, secret)
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(call.ArgsJSON), &args))
	config, ok := args["config"].(map[string]any)
	require.True(t, ok, "ordinary config preserved in the opsctl audit")
	assert.Equal(t, "10.0.0.1", config["host"])
	_, hasAuthType := config["auth_type"]
	assert.False(t, hasAuthType, "composite auth_type must be omitted from the opsctl audit")
}

// TestCmdUpdateAssetApprovalDetailUsesSafeProjectionWithOnlyFlagSpecifiedConfig 取代了旧版
// TestCmdUpdateAssetApprovalDetailCarriesOnlyFlagSpecifiedChanges：update asset 现在与
// createAsset 走同一条 prepare → 审批（SafeApprovalDetail）→ commit → put_asset 审计路径
// （E34 修复），不再把 flag 拼出的原始 params 直接序列化进 Detail——原始 params 一旦掺进
// --config/--config-file 提供的字段（集群/哨兵模式含 write-only 的 sentinel_password），
// 会把密钥整段带进审批文本。SafeApprovalDetail 只按类型白名单展示 name/type/config，且
// config 只含本次调用实际提供的字段（asset_put_svc.approvalView 的既有行为）。
func TestCmdUpdateAssetApprovalDetailUsesSafeProjectionWithOnlyFlagSpecifiedConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{
		{ID: 9, Name: "web-9", Type: asset_entity.AssetTypeSSH},
	})

	preserveCreateSeams(t)
	origWriter := opsctlAuditWriter
	opsctlAuditWriter = &mockAuditWriter{}
	t.Cleanup(func() { opsctlAuditWriter = origWriter })

	prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		prepared, err := asset_put_svc.Prepare(context.Background(), request)
		if err != nil {
			return nil, err
		}
		return &realProjectionPrepared{Prepared: prepared, result: &asset_put_svc.Result{ID: request.Asset.ID}}, nil
	}
	notifyAssetChanged = func() {}

	var approvalReq approval.ApprovalRequest
	requireUpdateApproval = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
		approvalReq = req
		return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-update"}, nil
	}

	run := func(t *testing.T, changeFlags ...string) map[string]any {
		t.Helper()
		approvalReq = approval.ApprovalRequest{}
		var stdout, stderr bytes.Buffer
		code := updateAsset(context.Background(), append([]string{"web-9"}, changeFlags...), "sess-update",
			commandIO{stdout: &stdout, stderr: &stderr})
		require.Equal(t, 0, code, stderr.String())
		require.Equal(t, "update", approvalReq.Type)
		require.Equal(t, int64(9), approvalReq.AssetID)
		require.Empty(t, approvalReq.Command, "Command must stay empty (non-empty wakes Stage-2 policy/grant checks)")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(approvalReq.Detail), &decoded),
			"Detail %q must carry the safe projection as JSON", approvalReq.Detail)
		return decoded
	}

	t.Run("全部连接变更 flag 都进入安全投影的 config", func(t *testing.T) {
		decoded := run(t, "--host", "10.0.0.2", "--port", "2222", "--username", "root")
		assert.ElementsMatch(t, []string{"name", "type", "config"}, mapKeys(decoded))
		assert.Equal(t, "web-9", decoded["name"])
		assert.Equal(t, "ssh", decoded["type"])
		config, ok := decoded["config"].(map[string]any)
		require.True(t, ok, "config must be an object, got %T", decoded["config"])
		assert.ElementsMatch(t, []string{"host", "port", "username"}, mapKeys(config))
		assert.Equal(t, "10.0.0.2", config["host"])
		assert.Equal(t, float64(2222), config["port"])
		assert.Equal(t, "root", config["username"])
	})

	t.Run("不带任何连接变更 flag：安全投影 config 为空", func(t *testing.T) {
		decoded := run(t)
		config, ok := decoded["config"].(map[string]any)
		require.True(t, ok, "config must still be an object, got %T", decoded["config"])
		assert.Empty(t, config)
	})
}

// TestCmdUpdateAssetExistingFlagsStillFeedRequestUnchanged 钉住"已有 flag 行为不变"：
// name/host/port/username/description/group-id/icon 依旧只在被传入时才改动目标资产，
// 未传入的字段保留原值——即便它们不再出现在 SafeApprovalDetail 里（上一个测试锁的是
// 审批视图，这个测试锁的是实际派给 asset_put_svc.Request 的内容）。
func TestCmdUpdateAssetExistingFlagsStillFeedRequestUnchanged(t *testing.T) {
	// resolveAsset 返回的是 AssetRepo 里那份实体的指针；updateAsset 直接在它上面改字段
	// （与生产行为一致），所以每个子用例都要有一份自己的 fixture，不能跨子用例共享同一个
	// *asset_entity.Asset——否则前一个子用例的改名会让后一个子用例按旧名字找不到资产。
	newHarness := func(t *testing.T, asset *asset_entity.Asset) (capturedAsset **asset_entity.Asset, capturedConfig *map[string]any, notified *bool) {
		t.Helper()
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{asset})

		preserveCreateSeams(t)
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = &mockAuditWriter{}
		t.Cleanup(func() { opsctlAuditWriter = origWriter })

		capturedAsset = new(*asset_entity.Asset)
		capturedConfig = new(map[string]any)
		prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
			*capturedAsset = request.Asset
			*capturedConfig = request.Config
			return &fakePreparedAssetCreate{
				approval: map[string]any{"name": request.Asset.Name, "type": request.Asset.Type},
				result:   &asset_put_svc.Result{ID: request.Asset.ID},
			}, nil
		}
		requireUpdateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
			return ApprovalResult{Decision: aictx.Allow}, nil
		}
		notified = new(bool)
		notifyAssetChanged = func() { *notified = true }
		return
	}

	t.Run("全部 flag 都传：全部字段按 flag 改写", func(t *testing.T) {
		capturedAsset, capturedConfig, notified := newHarness(t, &asset_entity.Asset{
			ID: 9, Name: "web-9", Type: asset_entity.AssetTypeSSH, Description: "old", GroupID: 1, Icon: "server",
		})
		var stdout, stderr bytes.Buffer
		code := updateAsset(context.Background(), []string{
			"web-9", "--name", "New Name", "--host", "10.0.0.2", "--port", "2222",
			"--username", "root", "--description", "edge box", "--group-id", "3", "--icon", "kubernetes",
		}, "sess-update", commandIO{stdout: &stdout, stderr: &stderr})
		require.Equal(t, 0, code, stderr.String())
		require.NotNil(t, *capturedAsset)
		assert.Equal(t, "New Name", (*capturedAsset).Name)
		assert.Equal(t, "edge box", (*capturedAsset).Description)
		assert.Equal(t, int64(3), (*capturedAsset).GroupID)
		assert.Equal(t, "kubernetes", (*capturedAsset).Icon)
		assert.Equal(t, "10.0.0.2", (*capturedConfig)["host"])
		assert.Equal(t, float64(2222), (*capturedConfig)["port"])
		assert.Equal(t, "root", (*capturedConfig)["username"])
		assert.True(t, *notified)
	})

	t.Run("不传任何 flag：既有字段原样保留", func(t *testing.T) {
		capturedAsset, capturedConfig, _ := newHarness(t, &asset_entity.Asset{
			ID: 9, Name: "web-9", Type: asset_entity.AssetTypeSSH, Description: "old", GroupID: 1, Icon: "server",
		})
		var stdout, stderr bytes.Buffer
		code := updateAsset(context.Background(), []string{"web-9"}, "sess-update",
			commandIO{stdout: &stdout, stderr: &stderr})
		require.Equal(t, 0, code, stderr.String())
		require.NotNil(t, *capturedAsset)
		assert.Equal(t, "web-9", (*capturedAsset).Name)
		assert.Equal(t, "old", (*capturedAsset).Description)
		assert.Equal(t, int64(1), (*capturedAsset).GroupID)
		assert.Equal(t, "server", (*capturedAsset).Icon)
		assert.Empty(t, *capturedConfig)
	})
}

// TestCmdUpdateAssetConfigFlagAppliesRedisModeFieldsAfterApproval 是 E34 的核心复现/回归
// 用例："opsctl update asset rc-cluster --config '{"mode":"cluster",...}'" 此前直接命中
// Go flag 的 "flag provided but not defined: -config"（退出码 2，update asset 从未开放这个
// flag）。用真实 asset_put_svc.Prepare/Commit（不打桩）+ 打桩的 AssetRepo 证明：--config 现在
// 能把 mode/nodes/node_address_map 写到已存在的 Redis 资产上，且沿用同一条 prepare → 审批 →
// commit → put_asset 审计路径（不经 callHandler 的原始 JSON 审计）。
func TestCmdUpdateAssetConfigFlagAppliesRedisModeFieldsAfterApproval(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	existing := &asset_entity.Asset{ID: 9, Name: "rc-cluster", Type: asset_entity.AssetTypeRedis}
	require.NoError(t, existing.SetRedisConfig(&asset_entity.RedisConfig{
		Host: "10.0.0.1", Port: 6379, Username: "default",
	}))
	mockAsset := registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{existing})
	var updated *asset_entity.Asset
	mockAsset.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *asset_entity.Asset) error {
		updated = a
		return nil
	}).AnyTimes()

	preserveCreateSeams(t)
	origWriter := opsctlAuditWriter
	writer := &mockAuditWriter{}
	opsctlAuditWriter = writer
	t.Cleanup(func() { opsctlAuditWriter = origWriter })

	requireUpdateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notified := false
	notifyAssetChanged = func() { notified = true }

	ctx := dbutil.WithTransactionRunner(context.Background(),
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })

	var stdout, stderr bytes.Buffer
	code := updateAsset(ctx, []string{
		"rc-cluster", "--config",
		`{"mode":"cluster","nodes":["10.0.0.1:6379","10.0.0.2:6379"],"node_address_map":{"10.0.0.1:6379":"127.0.0.1:16379"}}`,
	}, "sess-update", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())

	require.NotNil(t, updated, "commit must reach AssetRepo.Update")
	cfg, err := updated.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, "cluster", cfg.Mode)
	assert.Equal(t, []string{"10.0.0.1:6379", "10.0.0.2:6379"}, cfg.Nodes)
	assert.Equal(t, "127.0.0.1:16379", cfg.NodeAddressMap["10.0.0.1:6379"])
	assert.Empty(t, cfg.Host, "cluster mode must not retain the standalone host")
	assert.True(t, notified)
	assert.Contains(t, stdout.String(), `"id": 9`)

	call := writer.lastCall()
	assert.Equal(t, "put_asset", call.ToolName)
}

// TestCmdUpdateAssetConfigAndConfigFileMutuallyExclusive 复用 create 的互斥规则：两个源
// 一起给，报错退出 1，且从不触达审批（approvalCalls 必须是 0）。
func TestCmdUpdateAssetConfigAndConfigFileMutuallyExclusive(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{
		{ID: 9, Name: "rc-cluster", Type: asset_entity.AssetTypeRedis},
	})

	preserveCreateSeams(t)
	approvalCalls := 0
	requireUpdateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		approvalCalls++
		return ApprovalResult{}, errors.New("must not be reached")
	}
	notifyAssetChanged = func() {}

	var stdout, stderr bytes.Buffer
	code := updateAsset(context.Background(), []string{
		"rc-cluster", "--config", `{"mode":"cluster"}`, "--config-file", "/tmp/does-not-matter.json",
	}, "sess-update", commandIO{stdout: &stdout, stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "mutually exclusive")
	assert.Zero(t, approvalCalls)
}

// TestCmdUpdateAssetUnknownConfigFieldRejectedBeforeApproval 证明 --config 里的结构性错误
// （未知字段名）在真实 Prepare() 里立即报出具体字段，且从不触达审批——与 create 侧的
// TestCreateAssetRedisModeErrorsExitOneWithoutInvokingApproval 同一条规则,只是入口换成 update。
func TestCmdUpdateAssetUnknownConfigFieldRejectedBeforeApproval(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{
		{ID: 9, Name: "rc-cluster", Type: asset_entity.AssetTypeRedis, Config: `{"mode":"cluster","nodes":["10.0.0.1:6379"]}`},
	})

	preserveCreateSeams(t)
	approvalCalls := 0
	requireUpdateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		approvalCalls++
		return ApprovalResult{}, errors.New("must not be reached")
	}
	notifyAssetChanged = func() {}

	var stdout, stderr bytes.Buffer
	code := updateAsset(context.Background(), []string{
		"rc-cluster", "--config", `{"bogus_field":"x"}`,
	}, "sess-update", commandIO{stdout: &stdout, stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "bogus_field")
	assert.Zero(t, approvalCalls)
}

// update 的 --config / --config-file 带任一 write-only 字段（含 Redis 的 sentinel_password，
// 而不只是 password）时，照常给出明文暴露提醒。
func TestCmdUpdateAssetWarnsOnPlaintextSentinelPassword(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"--config", []string{"--config", `{"sentinel_password":"s3cret"}`}, "plaintext supplied in argv"},
		{"--config-file", []string{"--config-file", "/tmp/update.json"}, "plaintext config files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{{
				ID: 9, Name: "rc-sentinel", Type: asset_entity.AssetTypeRedis,
				Config: `{"mode":"sentinel","nodes":["10.0.0.1:26379"],"master_name":"mymaster"}`,
			}})
			preserveCreateSeams(t)
			origWriter := opsctlAuditWriter
			opsctlAuditWriter = &mockAuditWriter{}
			t.Cleanup(func() { opsctlAuditWriter = origWriter })
			requireUpdateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{Decision: aictx.Deny}, errors.New("denied")
			}
			notifyAssetChanged = func() {}

			var stdout, stderr bytes.Buffer
			code := updateAsset(context.Background(), append([]string{"rc-sentinel"}, tc.args...), "sess-update",
				commandIO{stdout: &stdout, stderr: &stderr, readFile: func(string) ([]byte, error) {
					return []byte(`{"sentinel_password":"s3cret"}`), nil
				}})
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr.String(), tc.want)
		})
	}
}

// TestCmdUpdateAssetRedisConfigApprovalAndAuditExcludeSentinelPassword 用真实 Prepare()
// （不打桩）证明 sentinel_password 这类 write-only 密钥既不出现在审批 Detail 里，也不出现在
// put_asset 审计的 ArgsJSON/Result 里——同一份 asset_put_svc 去密投影（approvalView 的
// ApprovalFields 白名单不含 sentinel_password/password/credential_id）覆盖 update，不需要
// update 自己重新实现一遍脱敏规则。
func TestCmdUpdateAssetRedisConfigApprovalAndAuditExcludeSentinelPassword(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{
		{ID: 9, Name: "rc-sentinel", Type: asset_entity.AssetTypeRedis, Config: `{"host":"10.0.0.1","port":6379}`},
	})

	preserveCreateSeams(t)
	writer := &mockAuditWriter{}
	origWriter := opsctlAuditWriter
	opsctlAuditWriter = writer
	t.Cleanup(func() { opsctlAuditWriter = origWriter })

	prepareAssetPut = func(_ context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		prepared, err := asset_put_svc.Prepare(context.Background(), request)
		if err != nil {
			return nil, err
		}
		return &realProjectionPrepared{Prepared: prepared, result: &asset_put_svc.Result{ID: request.Asset.ID}}, nil
	}
	var approvalReq approval.ApprovalRequest
	requireUpdateApproval = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
		approvalReq = req
		return ApprovalResult{Decision: aictx.Allow}, nil
	}
	notifyAssetChanged = func() {}

	secret := "sentinel-top-secret"
	var stdout, stderr bytes.Buffer
	code := updateAsset(context.Background(), []string{
		"rc-sentinel", "--config",
		`{"mode":"sentinel","nodes":["10.0.0.1:26379"],"master_name":"mymaster","sentinel_username":"sentuser","sentinel_password":"` + secret + `"}`,
	}, "sess-update", commandIO{stdout: &stdout, stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())

	assert.NotContains(t, approvalReq.Detail, secret)
	assert.NotContains(t, approvalReq.Detail, "sentinel_password")
	assert.Contains(t, approvalReq.Detail, "mymaster")

	call := writer.lastCall()
	assert.NotContains(t, call.ArgsJSON, secret)
	assert.NotContains(t, call.Result, secret)
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(call.ArgsJSON), &args))
	config, ok := args["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sentinel", config["mode"])
	assert.Equal(t, "mymaster", config["master_name"])
	_, hasSentinelPassword := config["sentinel_password"]
	assert.False(t, hasSentinelPassword)
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestCreateAssetRedisModeErrorsRejectedBeforeApprovalNamingTheField 复现 E27:opsctl
// help 称「Validation and reference checks run before desktop approval」，但集群/哨兵
// 模式字段错误此前只在批准后的 commit 里被 validateRedis 发现。用真实的
// assettype.PrepareCreate（不打桩）证明 Prepare() 本身此时就已经报错，且指出具体字段。
func TestCreateAssetRedisModeErrorsRejectedBeforeApprovalNamingTheField(t *testing.T) {
	for _, tt := range []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{
			name:    "sentinel missing master_name",
			config:  map[string]any{"mode": "sentinel", "nodes": []any{"10.0.0.1:26379"}},
			wantErr: "master_name",
		},
		{
			name: "node_address_map target has no port",
			config: map[string]any{
				"mode": "cluster", "nodes": []any{"10.0.0.1:6379"},
				"node_address_map": map[string]any{"10.0.0.1:6379": "bad-no-port"},
			},
			wantErr: "node_address_map",
		},
		{
			name:    "node without a port",
			config:  map[string]any{"mode": "cluster", "nodes": []any{"nohostport"}},
			wantErr: "nodes",
		},
		{
			name:    "unknown mode",
			config:  map[string]any{"mode": "weird", "host": "x"},
			wantErr: "mode",
		},
		{
			name:    "cluster redis_db must stay 0",
			config:  map[string]any{"mode": "cluster", "nodes": []any{"10.0.0.1:6379"}, "redis_db": 3},
			wantErr: "redis_db",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, _, err := parseAssetCreateForTest(t, createArgs(t, "redis", tt.config), nil, nil)
			require.NoError(t, err)
			_, err = asset_put_svc.Prepare(context.Background(), asset_put_svc.Request{Asset: request.asset, Config: request.config})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestCreateAssetRedisModeErrorsExitOneWithoutInvokingApproval drives the full createAsset
// command (real prepareAssetPut) and asserts the desktop approval hook is never reached for
// any of the four E27 mode error shapes — the bug let all four through to approval/commit.
// requireCreateApproval denies rather than allows: if the pre-approval reject regresses, this
// must fail on the approvalCalls assertion below rather than falling through to a real
// dbutil.WithTransaction commit with no test database configured.
func TestCreateAssetRedisModeErrorsExitOneWithoutInvokingApproval(t *testing.T) {
	preserveCreateSeams(t)
	approvalCalls := 0
	requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		approvalCalls++
		return ApprovalResult{}, errors.New("operation denied")
	}
	notifyAssetChanged = func() {}

	for _, tt := range []struct {
		name   string
		config map[string]any
	}{
		{name: "sentinel missing master_name", config: map[string]any{"mode": "sentinel", "nodes": []any{"10.0.0.1:26379"}}},
		{name: "node_address_map target has no port", config: map[string]any{
			"mode": "cluster", "nodes": []any{"10.0.0.1:6379"},
			"node_address_map": map[string]any{"10.0.0.1:6379": "bad-no-port"},
		}},
		{name: "node without a port", config: map[string]any{"mode": "cluster", "nodes": []any{"nohostport"}}},
		{name: "unknown mode", config: map[string]any{"mode": "weird", "host": "x"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := createAsset(context.Background(), createArgs(t, "redis", tt.config), "session", commandIO{stdout: &stdout, stderr: &stderr})
			assert.Equal(t, 1, code, stderr.String())
			assert.NotEmpty(t, stderr.String())
		})
	}
	assert.Zero(t, approvalCalls, "mode errors must be rejected before desktop approval, not after")
}

// TestCmdUpdateAssetRedisModeErrorsRejectedBeforeApprovalNamingTheField 与 create 同一条规则
// (spec「校验与桌面表单相同，失败时报出具体字段」)：update 的模式字段错误在审批前报出并指出
// 字段，审批从不被触达，也不写入。部分更新先叠加到已存储的配置上再校验——例如集群资产只改
// mode=sentinel 时，集群种子节点不会被当作哨兵节点沿用。requireUpdateApproval 用拒绝而不是
// 放行：若审批前拒绝回退，断言 approvalCalls 失败，而不是落到没有 AssetRepo.Update 期望的提交。
func TestCmdUpdateAssetRedisModeErrorsRejectedBeforeApprovalNamingTheField(t *testing.T) {
	for _, tt := range []struct {
		name    string
		config  string
		wantErr string
	}{
		{name: "sentinel missing master_name", config: `{"mode":"sentinel","nodes":["10.0.0.1:26379"]}`, wantErr: "master_name"},
		{name: "switching mode does not reuse the other mode's nodes", config: `{"mode":"sentinel","master_name":"mymaster"}`, wantErr: "nodes"},
		{name: "node_address_map target has no port", config: `{"node_address_map":{"10.0.0.1:6379":"bad-no-port"}}`, wantErr: "node_address_map"},
		{name: "node without a port", config: `{"nodes":["nohostport"]}`, wantErr: "nodes"},
		{name: "unknown mode", config: `{"mode":"weird"}`, wantErr: "mode"},
		{name: "cluster redis_db must stay 0", config: `{"redis_db":3}`, wantErr: "redis_db"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			existing := &asset_entity.Asset{ID: 9, Name: "rc-cluster", Type: asset_entity.AssetTypeRedis}
			require.NoError(t, existing.SetRedisConfig(&asset_entity.RedisConfig{
				Mode: asset_entity.RedisModeCluster, Nodes: []string{"10.0.0.1:6379"}, Username: "default",
			}))
			registerMockAssetRepo(t, ctrl, []*asset_entity.Asset{existing})

			preserveCreateSeams(t)
			origWriter := opsctlAuditWriter
			opsctlAuditWriter = &mockAuditWriter{}
			t.Cleanup(func() { opsctlAuditWriter = origWriter })
			approvalCalls := 0
			requireUpdateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
				approvalCalls++
				return ApprovalResult{}, errors.New("operation denied")
			}
			notifyAssetChanged = func() { t.Fatal("rejected update must not notify") }

			var stdout, stderr bytes.Buffer
			code := updateAsset(context.Background(), []string{"rc-cluster", "--config", tt.config}, "sess-update",
				commandIO{stdout: &stdout, stderr: &stderr})
			assert.Equal(t, 1, code, stderr.String())
			assert.Contains(t, stderr.String(), tt.wantErr)
			assert.Zero(t, approvalCalls, "mode errors must be rejected before approval, not after")
		})
	}
}

// TestCreateAssetRedisApprovalDetailIncludesNodeAddressMapWithoutInjectedPort 复现 E27 的
// approvalView 部分:node_address_map 不含密钥却被复合值判定整体丢弃，而 normalizeDefaultPort
// 对所有模式都注入 port:6379。用真实 Prepare() 验证审批详情的 config 视图。
func TestCreateAssetRedisApprovalDetailIncludesNodeAddressMapWithoutInjectedPort(t *testing.T) {
	for _, tt := range []struct {
		name        string
		config      map[string]any
		expectedMap map[string]string
	}{
		{
			name: "cluster",
			config: map[string]any{
				"mode": "cluster", "nodes": []any{"10.0.0.1:6379"},
				"node_address_map": map[string]any{"10.0.0.1:6379": "127.0.0.1:16379"},
			},
			expectedMap: map[string]string{"10.0.0.1:6379": "127.0.0.1:16379"},
		},
		{
			name: "sentinel",
			config: map[string]any{
				"mode": "sentinel", "nodes": []any{"10.0.0.1:26379"}, "master_name": "mymaster",
				"node_address_map": map[string]any{"10.0.0.1:26379": "127.0.0.1:36379"},
			},
			expectedMap: map[string]string{"10.0.0.1:26379": "127.0.0.1:36379"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, _, err := parseAssetCreateForTest(t, createArgs(t, "redis", tt.config), nil, nil)
			require.NoError(t, err)
			prepared, err := asset_put_svc.Prepare(context.Background(), asset_put_svc.Request{Asset: request.asset, Config: request.config})
			require.NoError(t, err)
			approvalConfig := prepared.SafeApprovalDetail()["config"].(map[string]any)
			assert.Equal(t, tt.expectedMap, approvalConfig["node_address_map"])
			_, hasPort := approvalConfig["port"]
			assert.False(t, hasPort, "cluster/sentinel approval config must not carry an injected port:6379")
		})
	}
}

// TestCmdCreateAssetWarnsOnPlaintextSentinelPassword verifies that create asset
// --config with a write-only field like sentinel_password prints the plaintext-exposure
// warning, just like update does. The warning should be printed once even when both
// --password and an inline secret are given.
func TestCmdCreateAssetWarnsOnPlaintextSentinelPassword(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		wantWarning   string
		shouldNotWarn bool
	}{
		{
			name:        "--config with sentinel_password",
			args:        []string{"--type", "redis", "--name", "rc-sentinel", "--config", `{"mode":"sentinel","nodes":["10.0.0.1:26379"],"master_name":"mymaster","sentinel_password":"s3cret"}`},
			wantWarning: "plaintext supplied in argv",
		},
		{
			name:          "--config without secrets",
			args:          []string{"--type", "redis", "--name", "rc-standalone", "--config", `{"host":"localhost","port":6379}`},
			shouldNotWarn: true,
		},
		{
			name:        "--config-file with sentinel_password should warn about config file, not argv",
			args:        []string{"--type", "redis", "--name", "rc-sentinel", "--config-file", "/tmp/sentinel.json"},
			wantWarning: "plaintext config files",
		},
		{
			name:        "both --password and --config with sentinel_password should warn once",
			args:        []string{"--type", "redis", "--name", "rc-sentinel", "--password", "nodepass", "--config", `{"mode":"sentinel","nodes":["10.0.0.1:26379"],"master_name":"mymaster","sentinel_password":"sentpass"}`},
			wantWarning: "plaintext supplied in argv",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preserveCreateSeams(t)
			origWriter := opsctlAuditWriter
			opsctlAuditWriter = &mockAuditWriter{}
			t.Cleanup(func() { opsctlAuditWriter = origWriter })

			requireCreateApproval = func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{Decision: aictx.Deny}, errors.New("denied")
			}
			notifyAssetChanged = func() {}

			var stdout, stderr bytes.Buffer
			readFile := func(path string) ([]byte, error) {
				if path == "/tmp/sentinel.json" {
					return []byte(`{"mode":"sentinel","nodes":["10.0.0.1:26379"],"master_name":"mymaster","sentinel_password":"s3cret"}`), nil
				}
				return nil, errors.New("file not found")
			}
			code := createAsset(context.Background(), tc.args, "sess-create",
				commandIO{stdout: &stdout, stderr: &stderr, readFile: readFile})
			assert.Equal(t, 1, code, "create should fail due to approval denial")
			for _, secret := range []string{"s3cret", "sentpass", "nodepass"} {
				assert.NotContains(t, stderr.String(), secret, "warnings must not echo the plaintext itself")
			}
			if tc.shouldNotWarn {
				assert.NotContains(t, stderr.String(), "plaintext", "should not warn about plaintext")
				assert.NotContains(t, stderr.String(), "Warning")
			} else {
				assert.Contains(t, stderr.String(), tc.wantWarning, "should warn about plaintext in argv or config file")
				// Verify warning appears exactly once
				warningCount := strings.Count(stderr.String(), "Warning:")
				assert.Equal(t, 1, warningCount, "warning should appear exactly once")
			}
		})
	}
}
