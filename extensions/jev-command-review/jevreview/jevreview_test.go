package jevreview

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/extensions/jev-command-review/typesafe"
)

func TestFailedReturnsQuestionsAtOrAboveTheThresholdSorted(t *testing.T) {
	scores := map[string]float64{QuestionRemoteCode: 0.9, QuestionDestructive: 0.5, QuestionDisruptive: 0.49}
	assert.Equal(t, []string{QuestionDestructive, QuestionRemoteCode}, Failed(scores, 0.5))
	assert.Empty(t, Failed(scores, 0.95))
}

func TestFailReason(t *testing.T) {
	assert.Equal(t, ReasonTimeout, FailReason(fmt.Errorf("evaluate: %w", context.DeadlineExceeded)))
	assert.Equal(t, ReasonInvalidAPIKey, FailReason(&typesafe.APIError{StatusCode: 401}))
	assert.Equal(t, ReasonUnavailable, FailReason(&typesafe.APIError{StatusCode: 500}))
	assert.Equal(t, ReasonUnavailable, FailReason(fmt.Errorf("connection reset")))
}
