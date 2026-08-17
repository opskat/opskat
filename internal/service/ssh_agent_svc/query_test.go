package ssh_agent_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo/mock_ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/sshagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func registerQueryRepos(t *testing.T) (*mock_ssh_agent_source_repo.MockSSHAgentSourceRepo, *mock_asset_repo.MockAssetRepo) {
	t.Helper()
	ctrl := gomock.NewController(t)
	sourceRepo := mock_ssh_agent_source_repo.NewMockSSHAgentSourceRepo(ctrl)
	assetRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
	originalSource := ssh_agent_source_repo.SSHAgentSource()
	originalAsset := asset_repo.Asset()
	ssh_agent_source_repo.RegisterSSHAgentSource(sourceRepo)
	asset_repo.RegisterAsset(assetRepo)
	t.Cleanup(func() {
		ssh_agent_source_repo.RegisterSSHAgentSource(originalSource)
		asset_repo.RegisterAsset(originalAsset)
	})
	return sourceRepo, assetRepo
}

func TestSourceMetadataNeverContainsEndpointValue(t *testing.T) {
	repo, _ := registerQueryRepos(t)

	repo.EXPECT().List(gomock.Any()).Return([]*ssh_agent_source_entity.SSHAgentSource{{
		ID: 4, Name: "work", EndpointType: "environment", Endpoint: "SECRET_AGENT_ENV", Description: "desktop", Createtime: 10, Updatetime: 11,
	}}, nil)

	got, err := ListMetadata(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, SourceMetadata{ID: 4, Name: "work", EndpointType: "environment", Description: "desktop", Createtime: 10, Updatetime: 11}, got[0])
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "SECRET_AGENT_ENV")
}

func TestObserveAvailableSourceInspectsIdentitiesOnlyOnce(t *testing.T) {
	sourceRepo, assetRepo := registerQueryRepos(t)
	source := &ssh_agent_source_entity.SSHAgentSource{ID: 4, Name: "work", EndpointType: "test", Endpoint: "ignored"}
	sourceRepo.EXPECT().Find(gomock.Any(), int64(4)).Return(source, nil).Times(1)
	assetRepo.EXPECT().CountAgentAuthBySourceID(gomock.Any(), int64(4)).Return(int64(2), nil).Times(1)
	assetRepo.EXPECT().CountAgentAuthBySourceIDGroupByFingerprint(gomock.Any(), int64(4)).Return(map[string]int64{}, nil).Times(1)

	oldInspect := observeIdentities
	observeIdentities = func(context.Context, string, string) ([]IdentitySummary, error) {
		return []IdentitySummary{{Fingerprint: "SHA256:selected", Type: "ssh-ed25519", Comment: "safe comment"}}, nil
	}
	t.Cleanup(func() { observeIdentities = oldInspect })

	observation, err := Observe(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, ProbeOK, observation.Status)
	assert.Equal(t, int64(2), observation.Usages)
	assert.Equal(t, []IdentitySummary{{Fingerprint: "SHA256:selected", Type: "ssh-ed25519", Comment: "safe comment"}}, observation.Identities)
}

func TestObserveAvailableSourceReturnsPerIdentityUsage(t *testing.T) {
	sourceRepo, assetRepo := registerQueryRepos(t)
	source := &ssh_agent_source_entity.SSHAgentSource{ID: 4, Name: "work", EndpointType: "test", Endpoint: "ignored"}
	sourceRepo.EXPECT().Find(gomock.Any(), int64(4)).Return(source, nil)
	assetRepo.EXPECT().CountAgentAuthBySourceID(gomock.Any(), int64(4)).Return(int64(3), nil)
	assetRepo.EXPECT().CountAgentAuthBySourceIDGroupByFingerprint(gomock.Any(), int64(4)).Return(map[string]int64{
		"SHA256:selected": 2,
	}, nil)

	oldInspect := observeIdentities
	observeIdentities = func(context.Context, string, string) ([]IdentitySummary, error) {
		return []IdentitySummary{
			{Fingerprint: "SHA256:selected", Type: "ssh-ed25519", Comment: "selected"},
			{Fingerprint: "SHA256:unused", Type: "ssh-rsa", Comment: "unused"},
		}, nil
	}
	t.Cleanup(func() { observeIdentities = oldInspect })

	observation, err := Observe(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, int64(3), observation.Usages)
	assert.Equal(t, int64(2), observation.Identities[0].Usages)
	assert.Zero(t, observation.Identities[1].Usages)
}

func TestObserveDegradesExpectedRuntimeFailureButPropagatesUsageError(t *testing.T) {
	sourceRepo, assetRepo := registerQueryRepos(t)
	source := &ssh_agent_source_entity.SSHAgentSource{ID: 4, Name: "offline", EndpointType: "test", Endpoint: "ignored"}
	sourceRepo.EXPECT().Find(gomock.Any(), int64(4)).Return(source, nil)
	assetRepo.EXPECT().CountAgentAuthBySourceID(gomock.Any(), int64(4)).Return(int64(2), nil)

	oldInspect := observeIdentities
	observeIdentities = func(context.Context, string, string) ([]IdentitySummary, error) {
		return nil, &sshagent.Error{Code: sshagent.CodeEndpointUnavailable, Message: "agent unavailable"}
	}
	t.Cleanup(func() { observeIdentities = oldInspect })

	observation, err := Observe(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, ProbeUnavailable, observation.Status)
	assert.Equal(t, int64(2), observation.Usages)
	assert.Empty(t, observation.Identities)

	repoErr := errors.New("database unavailable")
	sourceRepo.EXPECT().Find(gomock.Any(), int64(4)).Return(source, nil)
	assetRepo.EXPECT().CountAgentAuthBySourceID(gomock.Any(), int64(4)).Return(int64(0), repoErr)
	_, err = Observe(context.Background(), 4)
	assert.ErrorIs(t, err, repoErr)
}

func TestObservePropagatesCancellationInsteadOfReportingAgentUnavailable(t *testing.T) {
	sourceRepo, assetRepo := registerQueryRepos(t)
	sourceRepo.EXPECT().Find(gomock.Any(), int64(4)).Return(&ssh_agent_source_entity.SSHAgentSource{
		ID: 4, Name: "work", EndpointType: "test", Endpoint: "ignored",
	}, nil)
	assetRepo.EXPECT().CountAgentAuthBySourceID(gomock.Any(), int64(4)).Return(int64(2), nil)

	oldInspect := observeIdentities
	observeIdentities = func(context.Context, string, string) ([]IdentitySummary, error) {
		return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "caller stopped waiting"}
	}
	t.Cleanup(func() { observeIdentities = oldInspect })

	observation, err := Observe(context.Background(), 4)
	assert.Empty(t, observation)
	require.Error(t, err)
	code, ok := sshagent.CodeOf(err)
	assert.True(t, ok)
	assert.Equal(t, sshagent.CodeCancelled, code)
}
