package extension

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/command_review_svc/reviewerreg"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

func TestCheckSelectableReviewer(t *testing.T) {
	reviewers := []command_review_svc.ReviewerInfo{
		{Name: "ok", Usable: true, Signed: true},
		{Name: "unsigned", Usable: true},
		{Name: "tampered", Reason: command_review_svc.UnusableDigestMismatch},
	}
	assert.NoError(t, checkSelectableReviewer(reviewers, "ok"))
	assert.NoError(t, checkSelectableReviewer(reviewers, "unsigned"))
	assert.Error(t, checkSelectableReviewer(reviewers, "tampered"))
	assert.Error(t, checkSelectableReviewer(reviewers, "missing"))
}

// loadConfigOnce loads the app config once per test binary (bootstrap.LoadConfig
// runs once per process) into a directory that outlives every test.
var loadConfigOnce sync.Once

func newReviewBindings(t *testing.T) *Extension {
	t.Helper()
	loadConfigOnce.Do(func() {
		dir, err := os.MkdirTemp("", "opskat-review-bindings-*")
		require.NoError(t, err)
		_, err = bootstrap.LoadConfig(dir)
		require.NoError(t, err)
	})
	// The eligibility check is the service's; these tests stand in an eligible, unsigned one.
	reviewerreg.Default().SetEligibility(func(context.Context, string) (bool, error) { return false, nil })
	t.Cleanup(func() { reviewerreg.Default().SetEligibility(nil) })
	cfg := bootstrap.GetConfig()
	cfg.CommandReviewExtension = ""
	require.NoError(t, bootstrap.SaveConfig(cfg))
	return &Extension{ctx: context.Background(), lang: fixedLang("zh-CN")}
}

// 没有审核器时：状态里一个都没有并说明原因，选用或测试一个名字都说它不可用。
func TestCommandReviewBindingsWithNoReviewer(t *testing.T) {
	e := newReviewBindings(t)

	st, err := e.GetCommandReviewState()
	require.NoError(t, err)
	assert.NotNil(t, st.Reviewers, "an empty list must reach the frontend as [], not null")
	assert.Empty(t, st.Reviewers)
	assert.Empty(t, st.Active)
	assert.Equal(t, aictx.ReviewReasonNoReviewer, st.Problem)

	assert.ErrorContains(t, e.SetCommandReviewer("jev-command-review"), "jev-command-review")

	r, err := e.TestCommandReviewer("jev-command-review")
	require.NoError(t, err)
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, aictx.ReviewReasonReviewerUnavailable, r.Reason)
	assert.Equal(t, "jev-command-review", r.Extension)
}

type runningView struct{ extension.ExtensionView }

func (runningView) Plugin() *extension.Plugin { return &extension.Plugin{} }

// attachReviewer attaches extension name, contributing a reviewer, to the
// process's reviewer registry — what loading it does — until the test ends.
func attachReviewer(t *testing.T, name string) {
	t.Helper()
	contract := extmanifest.Manifest{Name: name, Version: "0.3.0"}
	contract.Contributes.CommandReviewer = true
	m := extensiontest.Described(contract, extensiontest.Describe{
		I18n: extension.ManifestI18n{DisplayName: "displayName"},
		Contributes: map[string]any{
			extwire.KeyTools: []extwire.Tool{{Name: "review", HostOnly: true, Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"assetType": map[string]any{"type": "string"}, "command": map[string]any{"type": "string"}, "syntax": map[string]any{"type": "string"},
				},
				"required": []any{"assetType", "command", "syntax"},
			}}},
			extwire.KeyCommandReviewer: extwire.CommandReviewer{Tool: "review", MaxCommandLen: 100,
				Questions: []extwire.ReviewQuestion{{ID: "destructive", Names: map[string]string{"en": "destructive"}}}},
		},
	})
	v := (&extension.ManifestInfo{Name: name, Manifest: m, Locales: map[string]map[string]string{
		"zh-cn": {"displayName": name + " 审核"},
	}}).View()
	registry := extension.NewContributionRegistry(reviewerreg.Default().Point(), extensiontestPass(extwire.KeyTools))
	require.NoError(t, registry.Attach(runningView{v}))
	t.Cleanup(func() { registry.Revoke(name) })
}

type extensiontestPass string

func (p extensiontestPass) Key() string                                  { return string(p) }
func (extensiontestPass) Check(extension.ExtensionView) error            { return nil }
func (extensiontestPass) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }

// The settings page reads and selects over the reviewer registry: an attached
// candidate is listed (display name in the app's language) and in use; selecting
// it saves the choice, selecting a name that is no candidate is refused.
func TestCommandReviewBindingsOverTheRegistry(t *testing.T) {
	e := newReviewBindings(t)
	attachReviewer(t, "alpha")

	st, err := e.GetCommandReviewState()
	require.NoError(t, err)
	assert.Equal(t, []command_review_svc.ReviewerInfo{{Name: "alpha", DisplayName: "alpha 审核", Version: "0.3.0", Usable: true,
		Questions: []command_review_svc.Question{{ID: "destructive", Names: map[string]string{"en": "destructive"}}}}}, st.Reviewers)
	assert.Equal(t, "alpha", st.Active)
	assert.Empty(t, st.Problem)

	attachReviewer(t, "beta")
	st, err = e.GetCommandReviewState()
	require.NoError(t, err)
	assert.Empty(t, st.Active, "two candidates and none selected")
	assert.Equal(t, aictx.ReviewReasonNoReviewer, st.Problem)

	require.NoError(t, e.SetCommandReviewer("beta"))
	assert.Equal(t, "beta", bootstrap.GetConfig().CommandReviewExtension)
	st, err = e.GetCommandReviewState()
	require.NoError(t, err)
	assert.Equal(t, "beta", st.Selected)
	assert.Equal(t, "beta", st.Active)

	assert.ErrorContains(t, e.SetCommandReviewer("gamma"), "gamma")
	assert.Equal(t, "beta", bootstrap.GetConfig().CommandReviewExtension, "a refused selection keeps the old one")
}
