package forward_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/forward_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/forward_repo"
	"github.com/opskat/opskat/internal/repository/forward_repo/mock_forward_repo"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type runtimeStub struct {
	running  bool
	stopped  []int64
	started  []int64
	startErr error
}

func (r *runtimeStub) StartConfig(_ context.Context, id int64) error {
	r.started = append(r.started, id)
	return r.startErr
}
func (r *runtimeStub) StopConfig(id int64)          { r.stopped = append(r.stopped, id) }
func (r *runtimeStub) IsConfigRunning(int64) bool   { return r.running }
func (r *runtimeStub) GetConfigStatus(int64) string { return "running" }
func (r *runtimeStub) GetRuleStatus(id int64) RuleStatus {
	return RuleStatus{RuleID: id, Status: "running"}
}

func setup(t *testing.T) (*mock_forward_repo.MockForwardRepo, *mock_asset_repo.MockAssetRepo) {
	t.Helper()
	ctrl := gomock.NewController(t)
	forwards := mock_forward_repo.NewMockForwardRepo(ctrl)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	originalForwards, originalAssets := forward_repo.Forward(), asset_repo.Asset()
	forward_repo.RegisterForward(forwards)
	asset_repo.RegisterAsset(assets)
	t.Cleanup(func() {
		forward_repo.RegisterForward(originalForwards)
		asset_repo.RegisterAsset(originalAssets)
	})
	return forwards, assets
}

func TestCreateValidatesAndPersistsConfigAndRules(t *testing.T) {
	forwards, _ := setup(t)
	svc := New(&runtimeStub{})
	svc.now = func() int64 { return 123 }
	rules := []forward_entity.ForwardRule{{Type: "local", LocalPort: 8080}}

	forwards.EXPECT().CreateConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c *forward_entity.ForwardConfig) error {
		require.Equal(t, &forward_entity.ForwardConfig{Name: "dev", AssetID: 7, Createtime: 123, Updatetime: 123}, c)
		c.ID = 11
		return nil
	})
	forwards.EXPECT().ReplaceRules(gomock.Any(), int64(11), gomock.Any()).DoAndReturn(func(_ context.Context, _ int64, got []*forward_entity.ForwardRule) error {
		require.Len(t, got, 1)
		require.Same(t, &rules[0], got[0])
		return nil
	})

	config, err := svc.Create(context.Background(), "dev", 7, rules)
	require.NoError(t, err)
	require.Equal(t, int64(11), config.ID)
}

func TestCreateRejectsInvalidConfigBeforePersistence(t *testing.T) {
	setup(t)
	_, err := New(&runtimeStub{}).Create(context.Background(), "", 7, nil)
	require.EqualError(t, err, "名称不能为空")
}

func TestUpdateRunningConfigStopsUpdatesAndRestarts(t *testing.T) {
	forwards, _ := setup(t)
	runtime := &runtimeStub{running: true}
	svc := New(runtime)
	svc.now = func() int64 { return 456 }
	config := &forward_entity.ForwardConfig{ID: 9, Name: "old", AssetID: 1, Createtime: 10, Updatetime: 10}
	rules := []forward_entity.ForwardRule{{Type: "remote", RemotePort: 22}}

	forwards.EXPECT().FindConfig(gomock.Any(), int64(9)).Return(config, nil)
	forwards.EXPECT().UpdateConfig(gomock.Any(), config).DoAndReturn(func(_ context.Context, got *forward_entity.ForwardConfig) error {
		require.Equal(t, "new", got.Name)
		require.Equal(t, int64(2), got.AssetID)
		require.Equal(t, int64(456), got.Updatetime)
		return nil
	})
	forwards.EXPECT().ReplaceRules(gomock.Any(), int64(9), gomock.Any()).Return(nil)

	got, err := svc.Update(context.Background(), 9, "new", 2, rules)
	require.NoError(t, err)
	require.Same(t, config, got)
	require.Equal(t, []int64{9}, runtime.stopped)
	require.Equal(t, []int64{9}, runtime.started)
}

func TestUpdateRestartFailureDoesNotFailSuccessfulPersistence(t *testing.T) {
	forwards, _ := setup(t)
	runtime := &runtimeStub{running: true, startErr: errors.New("occupied")}
	config := &forward_entity.ForwardConfig{ID: 9, Name: "old", AssetID: 1}
	forwards.EXPECT().FindConfig(gomock.Any(), int64(9)).Return(config, nil)
	forwards.EXPECT().UpdateConfig(gomock.Any(), config).Return(nil)
	forwards.EXPECT().ReplaceRules(gomock.Any(), int64(9), gomock.Any()).Return(nil)

	_, err := New(runtime).Update(context.Background(), 9, "new", 2, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{9}, runtime.started)
}

func TestDeleteStopsBeforeDeletingRulesAndConfig(t *testing.T) {
	forwards, _ := setup(t)
	runtime := &runtimeStub{}
	forwards.EXPECT().DeleteRulesByConfigID(gomock.Any(), int64(4)).Return(nil)
	forwards.EXPECT().DeleteConfig(gomock.Any(), int64(4)).Return(nil)

	require.NoError(t, New(runtime).Delete(context.Background(), 4))
	require.Equal(t, []int64{4}, runtime.stopped)
}

func TestListIncludesRuntimeStatusAndToleratesRelatedLookupFailures(t *testing.T) {
	forwards, assets := setup(t)
	configs := []*forward_entity.ForwardConfig{{ID: 1, AssetID: 10}, {ID: 2, AssetID: 20}}
	rule := &forward_entity.ForwardRule{ID: 101, ConfigID: 1}
	forwards.EXPECT().ListConfigs(gomock.Any()).Return(configs, nil)
	forwards.EXPECT().ListRulesByConfigID(gomock.Any(), int64(1)).Return([]*forward_entity.ForwardRule{rule}, nil)
	forwards.EXPECT().ListRulesByConfigID(gomock.Any(), int64(2)).Return(nil, errors.New("rules unavailable"))
	assets.EXPECT().Find(gomock.Any(), int64(10)).Return(&asset_entity.Asset{Name: "server-a"}, nil)
	assets.EXPECT().Find(gomock.Any(), int64(20)).Return(nil, errors.New("asset unavailable"))

	got, err := New(&runtimeStub{}).List(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "server-a", got[0].AssetName)
	require.Equal(t, "running", got[0].Status)
	require.Equal(t, []RuleWithStatus{{ForwardRule: *rule, Status: "running"}}, got[0].Rules)
	require.Empty(t, got[1].AssetName)
	require.Empty(t, got[1].Rules)
}
