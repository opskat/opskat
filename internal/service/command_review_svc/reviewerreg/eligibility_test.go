package reviewerreg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// installed is the install facts of a package recorded as installed from source,
// whose files on disk do or do not still match the recorded digest.
func installed(source string, matches bool) extension_svc.Provenance {
	return extension_svc.Provenance{Recorded: true, Source: source, PackageSHA256: "sum-of-" + source, DigestMatches: matches}
}

// The registry decides from extension management's install facts whether a reviewer
// may be used: a release build takes only store installs, any other build also takes
// local and dev installs (unsigned); a package that no longer matches its recorded
// digest, or without an install record, is never used. An unusable reviewer fails
// the review with the reason, is listed unusable with it, and its tool never runs.
func TestRegistryJudgesTheInstallFacts(t *testing.T) {
	cases := []struct {
		name    string
		facts   extension_svc.Provenance
		err     error
		release bool
		reason  string // "" = usable
		signed  bool
		msg     string // in the review failure
	}{
		{name: "store on a release build", facts: installed(extension_svc.InstallOriginStore, true), release: true, signed: true},
		{name: "store on another build", facts: installed(extension_svc.InstallOriginStore, true), signed: true},
		{name: "local on a release build", facts: installed(extension_svc.InstallOriginLocal, true), release: true, reason: UnusableStoreRequired, msg: "release build"},
		{name: "dev on a release build", facts: installed(extension_svc.InstallOriginDev, true), release: true, reason: UnusableStoreRequired, msg: "installed from dev"},
		{name: "local on another build is unsigned", facts: installed(extension_svc.InstallOriginLocal, true)},
		{name: "dev on another build is unsigned", facts: installed(extension_svc.InstallOriginDev, true)},
		{name: "digest mismatch", facts: installed(extension_svc.InstallOriginStore, false), reason: UnusableDigestMismatch, msg: "digest does not match"},
		{name: "no install record", facts: extension_svc.Provenance{}, reason: UnusableNoInstallRecord, msg: "no install record"},
		{name: "a record without a source", facts: installed("", true), reason: UnusableNoInstallRecord, msg: "no recorded install source"},
		{name: "facts unreadable", err: errors.New("db closed"), reason: UnusableCheckFailed, msg: "db closed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			host := &fakeHost{answer: `{"outcome":"pass"}`}
			reg, contributions := newRegistry(t, host)
			reg.release = func() bool { return tc.release }
			reg.SetProvenance(func(context.Context, string) (extension_svc.Provenance, error) { return tc.facts, tc.err })
			require.NoError(t, contributions.Attach(reviewerView("jev", true)))
			src := reg.Selecting(func() string { return "" })

			st := src.State(ctx, "en")
			require.Len(t, st.Reviewers, 1)
			info := st.Reviewers[0]
			assert.Equal(t, tc.reason, info.Reason)
			assert.Equal(t, tc.reason == "", info.Usable)
			assert.Equal(t, tc.signed, info.Signed)

			rev, err := src.Current(ctx)
			if tc.reason == "" {
				require.NoError(t, err)
				assert.Equal(t, "jev", st.Active)
				assert.Equal(t, tc.facts.PackageSHA256, rev.Identity().PackageDigest, "the digest just checked keys the cache")
				return
			}
			require.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
			assert.ErrorContains(t, err, tc.msg)
			_, err = src.Reviewer(ctx, "jev")
			assert.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
			assert.Empty(t, st.Active)
			assert.Equal(t, aictx.ReviewReasonReviewerUnavailable, st.Problem)
			assert.Empty(t, host.calls)
		})
	}
}

// Without the install facts wired in, nothing is usable: a missing wiring must not
// let an unchecked reviewer pass commands.
func TestNoProvenanceFailsClosed(t *testing.T) {
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
	reg.SetProvenance(func(_ context.Context, name string) (extension_svc.Provenance, error) {
		checks = append(checks, name)
		facts := installed(extension_svc.InstallOriginLocal, true)
		facts.PackageSHA256 = fmt.Sprintf("sum-at-check-%d", len(checks))
		return facts, nil
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
	assert.Equal(t, "jev", rev.Identity().Name)
	assert.Equal(t, []string{"jev"}, act.calls)
	assert.Equal(t, []string{"jev", "jev"}, *checks)
	assert.Equal(t, "sum-at-check-2", rev.Identity().PackageDigest, "the digest of the package that was activated")

	// Already running now: the next review activates nothing again.
	_, err = reg.Selecting(func() string { return "" }).Current(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"jev"}, act.calls)
}

func TestIneligibleDescribeOnlyReviewerIsNotActivated(t *testing.T) {
	reg, act, _ := describeOnlyRegistry(t, &fakeHost{})
	reg.release = func() bool { return true }

	_, err := reg.Selecting(func() string { return "" }).Current(context.Background())

	require.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
	assert.ErrorContains(t, err, "release build")
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
