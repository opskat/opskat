package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/credential_query_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestToSafeViewSerial 防止 SerialHandler.SafeView 返回的字段被丢掉。
// 之前 toSafeView 只接 host/port/username/database/redis/k8s 等几个 key，
// 串口资产对 AI 暴露时拿不到 port_path / baud_rate 等关键参数。
func TestToSafeViewSerial(t *testing.T) {
	asset := &asset_entity.Asset{
		ID:   42,
		Name: "console-1",
		Type: asset_entity.AssetTypeSerial,
	}
	require.NoError(t, asset.SetSerialConfig(&asset_entity.SerialConfig{
		PortPath:    "/dev/ttyUSB0",
		BaudRate:    115200,
		DataBits:    8,
		StopBits:    "1",
		Parity:      "none",
		FlowControl: "hardware",
	}))

	v := toSafeView(asset)

	assert.Equal(t, "/dev/ttyUSB0", v.PortPath)
	assert.Equal(t, 115200, v.BaudRate)
	assert.Equal(t, 8, v.DataBits)
	assert.Equal(t, "1", v.StopBits)
	assert.Equal(t, "none", v.Parity)
	assert.Equal(t, "hardware", v.FlowControl)
	// 串口没有 host/port/username 概念，确认没有被错误映射。
	assert.Empty(t, v.Host)
	assert.Equal(t, 0, v.Port)
	assert.Empty(t, v.Username)
}

// TestToSafeViewAgentSSH 覆盖 AI 资产安全视图对 Agent 认证 SSH 资产的对称返回：
// agent_source_id 与 agent_key_fingerprint 出现在安全视图里（规格允许，与桌面保存
// 校验一致），但端点/公钥/备注等敏感字段绝不出现——SSHConfig 本就不携带它们，
// SafeView 也不能把它们加进来。
type fakeAssetAuthenticationService struct {
	requests []credential_query_svc.AssetAuthenticationRequest
	result   *credential_query_svc.AssetAuthentication
	err      error
}

func (f *fakeAssetAuthenticationService) GetAssetAuthentication(_ context.Context, request credential_query_svc.AssetAuthenticationRequest) (*credential_query_svc.AssetAuthentication, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func registerAssetHandlerDependencies(t *testing.T, repo *mock_asset_repo.MockAssetRepo, auth credential_query_svc.AssetAuthenticationService) {
	t.Helper()
	originalRepo := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	originalAuth := credential_query_svc.DefaultAssetAuthentication()
	credential_query_svc.RegisterAssetAuthentication(auth)
	t.Cleanup(func() {
		asset_repo.RegisterAsset(originalRepo)
		credential_query_svc.RegisterAssetAuthentication(originalAuth)
	})
}

func TestHandleGetAssetAddsManagedAuthenticationWithoutSecrets(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset := &asset_entity.Asset{ID: 11, Name: "box", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, asset.SetSSHConfig(&asset_entity.SSHConfig{
		Host: "10.0.0.1", Port: 22, Username: "root", AuthType: asset_entity.AuthTypeKey, CredentialID: 7,
	}))
	repo.EXPECT().Find(gomock.Any(), int64(11)).Return(asset, nil)
	auth := &fakeAssetAuthenticationService{result: &credential_query_svc.AssetAuthentication{
		Type: credential_entity.TypeSSHKey, Ref: "credential:7", Name: "deploy", Username: "root", Availability: credential_query_svc.AvailabilityStored,
	}}
	registerAssetHandlerDependencies(t, repo, auth)

	out, err := handleGetAsset(context.Background(), map[string]any{"id": int64(11)})
	require.NoError(t, err)
	assert.Equal(t, []credential_query_svc.AssetAuthenticationRequest{{Type: credential_entity.TypeSSHKey, Ref: "credential:7"}}, auth.requests)
	assert.Contains(t, out, `"authentication":{"type":"ssh_key","ref":"credential:7","name":"deploy","username":"root","availability":"stored"}`)
	for _, forbidden := range []string{"credential_id", "private_key", "public_key", "passphrase", "password"} {
		assert.NotContains(t, out, forbidden)
	}
}

func TestHandleGetAssetLegacyInlineAuthenticationDoesNotFabricateReference(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset := &asset_entity.Asset{ID: 12, Name: "legacy", Type: asset_entity.AssetTypeRedis}
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: "redis", Port: 6379, Password: "cipher-inline"}))
	repo.EXPECT().Find(gomock.Any(), int64(12)).Return(asset, nil)
	auth := &fakeAssetAuthenticationService{}
	registerAssetHandlerDependencies(t, repo, auth)

	out, err := handleGetAsset(context.Background(), map[string]any{"id": int64(12)})
	require.NoError(t, err)
	assert.Empty(t, auth.requests)
	assert.NotContains(t, out, "authentication")
	assert.NotContains(t, out, "credential:")
	assert.NotContains(t, out, "cipher-inline")
}

func TestHandleGetAssetAgentAuthenticationIsSafeAndPropagatesEnrichmentErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset := &asset_entity.Asset{ID: 13, Name: "agent-box", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, asset.SetSSHConfig(&asset_entity.SSHConfig{
		Host: "10.0.0.2", Port: 22, Username: "root", AuthType: asset_entity.AuthTypeAgent,
		AgentSourceID: 4, AgentKeyFingerprint: "SHA256:selected",
	}))
	repo.EXPECT().Find(gomock.Any(), int64(13)).Return(asset, nil).Times(2)
	auth := &fakeAssetAuthenticationService{result: &credential_query_svc.AssetAuthentication{
		Type: credential_query_svc.TypeSSHAgent, Ref: "agent-source:4", Name: "work", EndpointType: "unix_socket",
		Fingerprint: "SHA256:selected", Availability: credential_query_svc.AvailabilityOK, KeyType: "ssh-ed25519", Comment: "safe comment",
	}}
	registerAssetHandlerDependencies(t, repo, auth)

	out, err := handleGetAsset(context.Background(), map[string]any{"id": int64(13)})
	require.NoError(t, err)
	assert.Contains(t, out, `"authentication":{"type":"ssh_agent","ref":"agent-source:4","name":"work","endpoint_type":"unix_socket","fingerprint":"SHA256:selected","availability":"ok","key_type":"ssh-ed25519","comment":"safe comment"}`)
	assert.Contains(t, out, `"agent_source_id":4`)
	assert.Contains(t, out, `"agent_key_fingerprint":"SHA256:selected"`)
	for _, forbidden := range []string{"endpoint_value", "public_key", "private_key", "signature", "challenge", "password"} {
		assert.NotContains(t, out, forbidden)
	}

	auth.err = errors.New("database unavailable")
	_, err = handleGetAsset(context.Background(), map[string]any{"id": int64(13)})
	assert.EqualError(t, err, "database unavailable")
}

func TestHandleListAssetsDoesNotEnrichAuthentication(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	assets := make([]*asset_entity.Asset, 2)
	for i, credentialID := range []int64{7, 8} {
		asset := &asset_entity.Asset{ID: int64(i + 1), Name: "asset", Type: asset_entity.AssetTypeRedis}
		require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: "redis", Port: 6379, CredentialID: credentialID}))
		assets[i] = asset
	}
	repo.EXPECT().List(gomock.Any(), gomock.Any()).Return(assets, nil)
	auth := &fakeAssetAuthenticationService{err: errors.New("list must not enrich")}
	registerAssetHandlerDependencies(t, repo, auth)

	out, err := handleListAssets(context.Background(), map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, auth.requests)
	assert.NotContains(t, out, "authentication")
}

func TestToSafeViewAgentSSH(t *testing.T) {
	asset := &asset_entity.Asset{
		ID:   1,
		Name: "box",
		Type: asset_entity.AssetTypeSSH,
	}
	require.NoError(t, asset.SetSSHConfig(&asset_entity.SSHConfig{
		Host: "10.0.0.1", Port: 22, Username: "root",
		AuthType:            "agent",
		AgentSourceID:       7,
		AgentKeyFingerprint: "SHA256:abc",
	}))

	v := toSafeView(asset)

	assert.Equal(t, "agent", v.AuthType)
	assert.Equal(t, int64(7), v.AgentSourceID)
	assert.Equal(t, "SHA256:abc", v.AgentKeyFingerprint)

	// 安全视图绝不包含端点/公钥/备注/签名/挑战答案：连空字段都不该出现，
	// 避免未来有人把敏感值填进来。
	data, err := json.Marshal(v)
	require.NoError(t, err)
	for _, banned := range []string{"agent_source_endpoint", "agent_public_key", "agent_comment", "agent_signature", "agent_challenge_answers"} {
		assert.NotContains(t, string(data), banned)
	}
}
