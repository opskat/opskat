package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/internal/service/extension_svc"
)

func TestCheckSelectableReviewer(t *testing.T) {
	reviewers := []extension_svc.CommandReviewerInfo{
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
