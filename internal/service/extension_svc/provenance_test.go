package extension_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/pkg/appversion"
)

func nopLogger() *zap.Logger { return zap.NewNop() }

type memStateRepo struct {
	rows map[string]*extension_state_entity.ExtensionState
	fail bool
}

func (m *memStateRepo) Find(_ context.Context, name string) (*extension_state_entity.ExtensionState, error) {
	row, ok := m.rows[name]
	if !ok {
		return nil, errNotFound
	}
	cp := *row
	return &cp, nil
}

func (m *memStateRepo) FindAll(context.Context) ([]*extension_state_entity.ExtensionState, error) {
	out := make([]*extension_state_entity.ExtensionState, 0, len(m.rows))
	for _, row := range m.rows {
		cp := *row
		out = append(out, &cp)
	}
	return out, nil
}

func (m *memStateRepo) Create(_ context.Context, state *extension_state_entity.ExtensionState) error {
	if m.fail {
		return errNotFound
	}
	cp := *state
	m.rows[state.Name] = &cp
	return nil
}

func (m *memStateRepo) Update(_ context.Context, state *extension_state_entity.ExtensionState) error {
	if m.fail {
		return errNotFound
	}
	cp := *state
	m.rows[state.Name] = &cp
	return nil
}

func (m *memStateRepo) Delete(_ context.Context, name string) error {
	delete(m.rows, name)
	return nil
}

var errNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }

func TestReviewerEligible(t *testing.T) {
	store := &extension_state_entity.ExtensionState{Source: InstallOriginStore, PackageSHA256: "abc"}
	local := &extension_state_entity.ExtensionState{Source: InstallOriginLocal, PackageSHA256: "abc"}
	dev := &extension_state_entity.ExtensionState{Source: InstallOriginDev, PackageSHA256: "abc"}
	unknown := &extension_state_entity.ExtensionState{PackageSHA256: "abc"}

	release := appversion.Info{Kind: appversion.KindRelease, Version: "1.0.0"}
	nightly := appversion.Info{Kind: appversion.KindNightly, Version: "1.0.0-nightly.1"}
	devBuild := appversion.Info{Kind: appversion.KindDev, Version: "1.0.0"}

	assert.NoError(t, reviewerEligible(store, "abc", release))
	assert.Error(t, reviewerEligible(local, "abc", release), "正式版不接受本地安装的审核器")
	assert.Error(t, reviewerEligible(dev, "abc", release))
	assert.Error(t, reviewerEligible(unknown, "abc", release), "没有来源记录不能当审核器")

	err := reviewerEligible(local, "abc", nightly)
	assert.ErrorIs(t, err, ErrReviewerUnsigned)
	err = reviewerEligible(dev, "def", devBuild)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrReviewerUnsigned, "摘要不符不是未签名")

	assert.ErrorIs(t, reviewerEligible(local, "abc", devBuild), ErrReviewerUnsigned)
	assert.NoError(t, reviewerEligible(store, "abc", nightly))
}

func TestRecordProvenanceKeepsSourceAcrossEnable(t *testing.T) {
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{}}
	svc := &Service{stateRepo: repo, logger: nopLogger()}
	ctx := context.Background()

	require.NoError(t, svc.recordProvenance(ctx, "rev", InstallOriginStore, "deadbeef", true))
	got := repo.rows["rev"]
	assert.Equal(t, InstallOriginStore, got.Source)
	assert.Equal(t, "deadbeef", got.PackageSHA256)
	assert.True(t, got.Enabled)

	svc.ensureState(ctx, "rev", false)
	assert.Equal(t, InstallOriginStore, repo.rows["rev"].Source, "启用/停用不能清掉来源")
	assert.Equal(t, "deadbeef", repo.rows["rev"].PackageSHA256)

	require.NoError(t, svc.recordProvenance(ctx, "rev", InstallOriginLocal, "cafebabe", true))
	assert.Equal(t, InstallOriginLocal, repo.rows["rev"].Source)
	assert.Equal(t, "cafebabe", repo.rows["rev"].PackageSHA256)
}

func TestRecordProvenanceReturnsRepoError(t *testing.T) {
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{}, fail: true}
	svc := &Service{stateRepo: repo, logger: nopLogger()}
	err := svc.recordProvenance(context.Background(), "rev", InstallOriginStore, "abc", true)
	assert.Error(t, err)
}
