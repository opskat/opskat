package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
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
	oldNotify := notifyAssetChanged
	t.Cleanup(func() {
		prepareAssetPut = oldPrepare
		requireCreateApproval = oldApproval
		notifyAssetChanged = oldNotify
	})
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

// TestCmdUpdateAssetApprovalDetailCarriesOnlyFlagSpecifiedChanges 锁住 spec 决策 18
// （Problem 6）：update asset 的审批主体 Detail 必须带上本次实际变更的字段，未经
// flag 指定的字段不出现。Detail 是审批人看到的全部——桌面 OpsctlApprovalDialog 对
// 这类请求只渲染它，终端提示（renderTTYApprovalPrompt）也照抄，所以修的是生产者
// （cmdUpdate 构造 ApprovalRequest 的地方），不是提示侧的本地摘要。Command 依合同
// 保持为空：非空会唤醒 requireApproval 的 Stage-2 策略/grant 检查，而 update 没有
// 可被规则匹配的主体（spec 决策 17）。
func TestCmdUpdateAssetApprovalDetailCarriesOnlyFlagSpecifiedChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 9, Name: "web-9", Type: asset_entity.AssetTypeSSH},
	}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})

	origWriter := opsctlAuditWriter
	opsctlAuditWriter = &mockAuditWriter{}
	t.Cleanup(func() { opsctlAuditWriter = origWriter })

	handlers := map[string]tool.ToolHandlerFunc{
		"put_asset": func(context.Context, map[string]any) (string, error) {
			return `{"id":9,"message":"asset updated"}`, nil
		},
	}

	var approvalReq approval.ApprovalRequest
	origApproval := requireUpdateApproval
	requireUpdateApproval = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
		approvalReq = req
		return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-update"}, nil
	}
	t.Cleanup(func() { requireUpdateApproval = origApproval })

	run := func(t *testing.T, changeFlags ...string) map[string]any {
		t.Helper()
		approvalReq = approval.ApprovalRequest{}
		code := cmdUpdate(context.Background(), handlers, append([]string{"asset", "web-9"}, changeFlags...), "sess-update")
		require.Equal(t, 0, code)
		require.Equal(t, "update", approvalReq.Type)
		require.Equal(t, int64(9), approvalReq.AssetID)
		require.Empty(t, approvalReq.Command, "Command must stay empty (non-empty wakes Stage-2 policy/grant checks)")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(approvalReq.Detail), &decoded),
			"Detail %q must carry the change set as JSON, not just echo the command line", approvalReq.Detail)
		return decoded
	}

	t.Run("全部变更 flag 都进入 Detail", func(t *testing.T) {
		decoded := run(t,
			"--name", "New Name", "--host", "10.0.0.2", "--port", "2222",
			"--username", "root", "--description", "edge box",
			"--group-id", "3", "--icon", "server")
		assert.ElementsMatch(t,
			[]string{"asset", "name", "description", "group_id", "icon", "config"}, mapKeys(decoded))
		assert.Equal(t, "9", decoded["asset"])
		assert.Equal(t, "New Name", decoded["name"])
		assert.Equal(t, "edge box", decoded["description"])
		assert.Equal(t, float64(3), decoded["group_id"])
		assert.Equal(t, "server", decoded["icon"])
		config, ok := decoded["config"].(map[string]any)
		require.True(t, ok, "config must be an object, got %T", decoded["config"])
		assert.ElementsMatch(t, []string{"host", "port", "username"}, mapKeys(config))
		assert.Equal(t, "10.0.0.2", config["host"])
		assert.Equal(t, float64(2222), config["port"])
		assert.Equal(t, "root", config["username"])
	})

	t.Run("只改 group-id（0=移出组）：未经 flag 指定的字段不出现", func(t *testing.T) {
		decoded := run(t, "--group-id", "0")
		assert.ElementsMatch(t, []string{"asset", "group_id"}, mapKeys(decoded))
		assert.Equal(t, float64(0), decoded["group_id"])
	})

	t.Run("不带任何变更 flag：Detail 不含变更字段", func(t *testing.T) {
		decoded := run(t)
		assert.ElementsMatch(t, []string{"asset"}, mapKeys(decoded))
	})
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
