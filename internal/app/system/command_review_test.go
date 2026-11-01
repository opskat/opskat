package system

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/internal/service/command_review_svc"
)

func TestValidateCommandReviewTimeout(t *testing.T) {
	for _, ms := range []int{1000, 5000, 60000} {
		assert.NoError(t, validateCommandReviewTimeout(ms), ms)
	}
	for _, ms := range []int{0, 999, 60001} {
		assert.Error(t, validateCommandReviewTimeout(ms), ms)
	}
}

func TestNewCommandReviewStatus(t *testing.T) {
	assert.Equal(t, CommandReviewStatus{}, newCommandReviewStatus(command_review_svc.Status{}))

	at := time.UnixMilli(1700000000123)
	assert.Equal(t,
		CommandReviewStatus{LastFailReason: "timeout", LastFailAt: 1700000000123},
		newCommandReviewStatus(command_review_svc.Status{LastFailReason: "timeout", LastFailAt: at}))
}
