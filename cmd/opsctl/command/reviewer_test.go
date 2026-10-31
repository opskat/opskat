package command

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/internal/service/command_review_svc/reviewerreg"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

type runningReviewerView struct{ extension.ExtensionView }

func (runningReviewerView) Plugin() *extension.Plugin { return &extension.Plugin{} }

func reviewerExtView(name string, running bool) extension.ExtensionView {
	contract := extmanifest.Manifest{Name: name, Version: "1.0.0"}
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
	v := (&extension.ManifestInfo{Name: name, Manifest: m}).View()
	if running {
		return runningReviewerView{v}
	}
	return v
}

// restoreReviewerHooks puts back the activation and eligibility hooks after the test.
func restoreReviewerHooks(t *testing.T) {
	t.Helper()
	activate, eligible := activateExtensionFn, reviewerEligibilityFn
	t.Cleanup(func() { activateExtensionFn, reviewerEligibilityFn = activate, eligible })
}

// opsctl attaches the reviewer describe-only at startup; when a review is needed the
// registry checks eligibility first and only then activates that one extension
// through the same activation function exec uses.
func TestReviewerIsCheckedThenActivatedThroughTheExecActivationPath(t *testing.T) {
	restoreReviewerHooks(t)
	reviewerreg.Default().SetSettingsHasher(func(context.Context, string) (string, error) { return "h", nil })
	t.Cleanup(func() { reviewerreg.Default().SetSettingsHasher(nil) })

	var events []string
	reviewerEligibilityFn = func(_ context.Context, name string) (bool, error) {
		events = append(events, "eligible:"+name)
		return false, nil
	}
	activateExtensionFn = func(_ context.Context, name string) error {
		events = append(events, "activate:"+name)
		extensionContributions.Revoke(name)
		return extensionContributions.Attach(reviewerExtView(name, true))
	}
	require.NoError(t, extensionContributions.Attach(reviewerExtView("rv", false)))
	t.Cleanup(func() { extensionContributions.Revoke("rv") })
	src := reviewerreg.Default().Selecting(func() string { return "" })

	assert.Empty(t, events, "attaching describe-only activates nothing")
	rev, err := src.Current(context.Background())

	require.NoError(t, err)
	name, _, _ := rev.Identity()
	assert.Equal(t, "rv", name)
	assert.Equal(t, []string{"eligible:rv", "activate:rv", "eligible:rv"}, events)
}

func TestIneligibleReviewerIsNotActivatedByOpsctl(t *testing.T) {
	restoreReviewerHooks(t)
	activated := false
	activateExtensionFn = func(context.Context, string) error { activated = true; return nil }
	reviewerEligibilityFn = func(context.Context, string) (bool, error) {
		return false, errors.New("must be installed from the store on a release build")
	}
	require.NoError(t, extensionContributions.Attach(reviewerExtView("rv", false)))
	t.Cleanup(func() { extensionContributions.Revoke("rv") })

	_, err := reviewerreg.Default().Selecting(func() string { return "" }).Current(context.Background())

	require.ErrorIs(t, err, command_review_svc.ErrReviewerUnavailable)
	assert.ErrorContains(t, err, "release build")
	assert.False(t, activated)
}
