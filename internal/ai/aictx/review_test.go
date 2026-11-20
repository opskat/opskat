package aictx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBatchReviewPicksWhyAPersonWasAsked(t *testing.T) {
	failed := &ReviewInfo{Outcome: ReviewFail, Reason: "timeout"}
	rejected := &ReviewInfo{Outcome: ReviewReject, Failed: []string{"destructive"}}

	assert.Same(t, rejected, BatchReview([]*ReviewInfo{nil, failed, rejected}), "审核未通过的最能说明为什么问人")
	assert.Same(t, failed, BatchReview([]*ReviewInfo{nil, failed}))
	skipped := &ReviewInfo{Outcome: ReviewSkipped, Reason: ReviewReasonPipedInput}
	assert.Same(t, skipped, BatchReview([]*ReviewInfo{nil, skipped}), "没送审的原因同样说明为什么问人")
	assert.Same(t, rejected, BatchReview([]*ReviewInfo{skipped, rejected}))
	assert.Nil(t, BatchReview([]*ReviewInfo{nil, nil}), "都没审核过（默认模式）时不记审核结果")
}
