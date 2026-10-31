package reviewerreg

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// A reviewer the eligibility check refuses is never used: the review fails with the
// reason, the settings list shows it unusable with that reason, and its tool is
// never called.
func TestIneligibleReviewerIsNeverCalled(t *testing.T) {
	ctx := context.Background()
	host := &fakeHost{answer: `{"outcome":"pass"}`}
	reg, contributions := newRegistry(t, host)
	reg.SetEligibility(func(context.Context, string) (bool, error) {
		return false, &command_review_svc.IneligibleError{Code: command_review_svc.UnusableDigestMismatch, Err: errors.New("package digest does not match")}
	})
	require.NoError(t, contributions.Attach(reviewerView("jev", true)))
	src := reg.Selecting(func() string { return "" })

	_, err := src.Current(ctx)
	require.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
	assert.ErrorContains(t, err, "package digest does not match")
	_, err = src.Reviewer(ctx, "jev")
	assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)

	st := src.State(ctx, "en")
	require.Len(t, st.Reviewers, 1)
	assert.False(t, st.Reviewers[0].Usable)
	assert.False(t, st.Reviewers[0].Signed)
	assert.Equal(t, command_review_svc.UnusableDigestMismatch, st.Reviewers[0].Reason)
	assert.Empty(t, st.Active)
	assert.Equal(t, aictx.ReviewReasonReviewerUnavailable, st.Problem)
	assert.Empty(t, host.calls)
}

func TestEligibleReviewerSignedFlagFollowsEligibility(t *testing.T) {
	ctx := context.Background()
	for _, signed := range []bool{true, false} {
		reg, contributions := newRegistry(t, &fakeHost{})
		reg.SetEligibility(func(context.Context, string) (bool, error) { return signed, nil })
		require.NoError(t, contributions.Attach(reviewerView("jev", true)))

		st := reg.Selecting(func() string { return "" }).State(ctx, "en")
		require.Len(t, st.Reviewers, 1)
		assert.True(t, st.Reviewers[0].Usable, "unsigned local installs stay usable on non-release builds")
		assert.Equal(t, signed, st.Reviewers[0].Signed)
		assert.Equal(t, "jev", st.Active)
	}
}

// Without an eligibility check wired in, nothing is eligible: a missing wiring must
// not let an unchecked reviewer pass commands.
func TestNoEligibilityCheckFailsClosed(t *testing.T) {
	reg := New((&fakeHost{answer: `{"outcome":"pass"}`}).call)
	reg.SetSettingsHasher(func(context.Context, string) (string, error) { return "h", nil })
	require.NoError(t, extension.NewContributionRegistry(reg.Point(), passPoint("tools")).Attach(reviewerView("jev", true)))

	_, err := reg.Selecting(func() string { return "" }).Current(context.Background())
	assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
}

// activation is a stand-in for a process that attaches reviewers describe-only and
// brings one up when asked (opsctl): activating swaps the describe-only attach for a
// running one, as extension_svc.Activate does.
type activation struct {
	contributions *extension.ContributionRegistry
	calls         []string
	err           error
}

func (a *activation) activate(_ context.Context, name string) error {
	a.calls = append(a.calls, name)
	if a.err != nil {
		return a.err
	}
	a.contributions.Revoke(name)
	return a.contributions.Attach(reviewerView(name, true))
}

func describeOnlyRegistry(t *testing.T, host *fakeHost) (*Registry, *activation, *[]string) {
	t.Helper()
	reg, contributions := newRegistry(t, host)
	var checks []string
	act := &activation{contributions: contributions}
	reg.SetEligibility(func(_ context.Context, name string) (bool, error) {
		checks = append(checks, name)
		return false, nil
	})
	reg.SetActivator(act.activate)
	require.NoError(t, contributions.Attach(reviewerView("jev", false)))
	return reg, act, &checks
}

// The eligibility check runs before the extension is activated (so an ineligible
// package never runs), and again after (the files activated are the files checked).
func TestCurrentActivatesTheDescribeOnlyReviewerAfterTheEligibilityCheck(t *testing.T) {
	ctx := context.Background()
	host := &fakeHost{answer: `{"outcome":"pass"}`}
	reg, act, checks := describeOnlyRegistry(t, host)

	rev, err := reg.Selecting(func() string { return "" }).Current(ctx)

	require.NoError(t, err)
	name, _, _ := rev.Identity()
	assert.Equal(t, "jev", name)
	assert.Equal(t, []string{"jev"}, act.calls)
	assert.Equal(t, []string{"jev", "jev"}, *checks)

	// Already running now: the next review activates nothing again.
	_, err = reg.Selecting(func() string { return "" }).Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"jev"}, act.calls)
}

func TestIneligibleDescribeOnlyReviewerIsNotActivated(t *testing.T) {
	reg, act, _ := describeOnlyRegistry(t, &fakeHost{})
	reg.SetEligibility(func(context.Context, string) (bool, error) {
		return false, errors.New("installed from local on a release build")
	})

	_, err := reg.Selecting(func() string { return "" }).Current(context.Background())

	require.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
	assert.ErrorContains(t, err, "installed from local on a release build")
	assert.Empty(t, act.calls, "an ineligible extension's code never runs")
}

func TestActivationFailureIsUnavailable(t *testing.T) {
	reg, act, _ := describeOnlyRegistry(t, &fakeHost{})
	act.err = errors.New("extension \"jev\" is disabled in the desktop app")

	_, err := reg.Selecting(func() string { return "" }).Current(context.Background())

	require.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
	assert.ErrorContains(t, err, "disabled in the desktop app")
}

// Without an activator (the desktop app runs every enabled extension) a
// describe-only candidate stays unavailable.
func TestDescribeOnlyReviewerWithoutActivatorIsUnavailable(t *testing.T) {
	reg, contributions := newRegistry(t, &fakeHost{})
	require.NoError(t, contributions.Attach(reviewerView("jev", false)))
	_, err := reg.Selecting(func() string { return "" }).Current(context.Background())
	assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
}

// nopCache is a review cache that never hits.
type nopCache struct{}

func (nopCache) Get(context.Context, string) (*command_review_svc.Result, error) { return nil, nil }

func (nopCache) Put(context.Context, string, command_review_svc.Result, time.Duration) error {
	return nil
}

// Only a command that needs review activates anything: rule-decided commands and
// default-mode assets are decided without touching the reviewer source.
func TestOnlyCommandsNeedingReviewActivateTheReviewer(t *testing.T) {
	ctx := context.Background()
	host := &fakeHost{answer: `{"outcome":"pass"}`}
	reg, act, _ := describeOnlyRegistry(t, host)
	source := reg.Selecting(func() string { return "" })
	reviews := command_review_svc.New(func() time.Duration { return 3 * time.Second }, nopCache{}, source)
	permission.SetReviewer(reviews)
	t.Cleanup(func() { permission.SetReviewer(nil) })

	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	policy, err := json.Marshal(asset_entity.CommandPolicy{AllowList: []string{"ls *"}, DenyList: []string{"shutdown *"}})
	require.NoError(t, err)
	for id, mode := range map[int64]string{1: policyent.PermissionModeAssisted, 2: policyent.PermissionModeDefault} {
		assets.EXPECT().Find(gomock.Any(), id).Return(&asset_entity.Asset{
			ID: id, Type: asset_entity.AssetTypeSSH, PermissionMode: mode, CmdPolicy: string(policy),
		}, nil).AnyTimes()
	}
	check := func(id int64, command string) aictx.CheckResult {
		return permission.CheckPermission(ctx, asset_entity.AssetTypeSSH, id, command)
	}

	t.Run("a rule allow, a rule deny and a default-mode ask do not activate", func(t *testing.T) {
		assert.Equal(t, aictx.Allow, check(1, "ls /tmp").Decision)
		assert.Equal(t, aictx.Deny, check(1, "shutdown now").Decision)
		assert.Equal(t, aictx.NeedConfirm, check(2, "systemctl status nginx").Decision)
		assert.Empty(t, act.calls)
	})
	t.Run("a command the rules would ask about activates the reviewer once", func(t *testing.T) {
		got := check(1, "systemctl status nginx")
		assert.Equal(t, aictx.Allow, got.Decision)
		assert.Equal(t, []string{"jev"}, act.calls)
		check(1, "systemctl status sshd")
		assert.Equal(t, []string{"jev"}, act.calls, "already running")
	})
}
