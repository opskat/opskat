package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_put_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				stdin: &bytes.Buffer{}, stderr: &stderr, readFile: func(string) ([]byte, error) { return nil, errors.New("unexpected read") },
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
			stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr,
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
			request, _, err := parseAssetCreateForTest(t, tt.args, "", nil)
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
	}, "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
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
		code := createAsset(context.Background(), createArgs(t, "redis", validCreateConfig("redis")), "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
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
		code := createAsset(context.Background(), createArgs(t, "redis", validCreateConfig("redis")), "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
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
	}, "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
	require.Equal(t, 1, code)
	require.Len(t, writer.calls, 1)
	call := writer.lastCall()
	assert.Error(t, call.Error)
	assert.NotContains(t, call.ArgsJSON, "failure-top-secret")
	assert.NotContains(t, call.Error.Error(), "failure-top-secret")
	assert.Zero(t, notifyCalls)
	assert.Empty(t, stdout.String())
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
		stdin: &bytes.Buffer{}, stdout: failingWriter{err: errors.New("stdout closed")}, stderr: &stderr,
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
	}, "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
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
	}, "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
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
	}, "session", commandIO{stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr})
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
