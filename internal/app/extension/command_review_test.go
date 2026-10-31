package extension

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

func TestCheckSelectableReviewer(t *testing.T) {
	reviewers := []command_review_svc.ReviewerInfo{
		{Name: "ok", Usable: true, Signed: true},
		{Name: "unsigned", Usable: true},
		{Name: "tampered", Reason: "command reviewer package digest does not match the installed wasm"},
	}
	assert.NoError(t, checkSelectableReviewer(reviewers, "ok"))
	assert.NoError(t, checkSelectableReviewer(reviewers, "unsigned"))
	err := checkSelectableReviewer(reviewers, "tampered")
	assert.ErrorContains(t, err, "digest")
	assert.Error(t, checkSelectableReviewer(reviewers, "missing"))
}

// 没有审核器时：状态里一个都没有并说明原因，选用或测试一个名字都说它不可用。
func TestCommandReviewBindingsWithNoReviewer(t *testing.T) {
	e := &Extension{ctx: context.Background()}

	st, err := e.GetCommandReviewState()
	require.NoError(t, err)
	assert.NotNil(t, st.Reviewers, "an empty list must reach the frontend as [], not null")
	assert.Empty(t, st.Reviewers)
	assert.Empty(t, st.Active)
	assert.Equal(t, command_review_svc.ReasonNoReviewer, st.Problem)

	assert.ErrorContains(t, e.SetCommandReviewer("jev-command-review"), "jev-command-review")

	r, err := e.TestCommandReviewer("jev-command-review")
	require.NoError(t, err)
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, command_review_svc.ReasonReviewerUnavailable, r.Reason)
	assert.Equal(t, "jev-command-review", r.Extension)
}
