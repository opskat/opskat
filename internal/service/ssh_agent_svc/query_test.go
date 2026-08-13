package ssh_agent_svc

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo/mock_ssh_agent_source_repo"
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

func TestObserveDegradesExpectedRuntimeFailureButPropagatesUsageError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket unavailable fixture is platform-specific")
	}
	sourceRepo, assetRepo := registerQueryRepos(t)
	source := &ssh_agent_source_entity.SSHAgentSource{ID: 4, Name: "offline", EndpointType: "unix_socket", Endpoint: "/tmp/definitely-not-an-agent-opskat"}
	sourceRepo.EXPECT().Find(gomock.Any(), int64(4)).Return(source, nil)
	assetRepo.EXPECT().CountAgentAuthBySourceID(gomock.Any(), int64(4)).Return(int64(2), nil)

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
