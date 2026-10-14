package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateCommandReviewTimeout(t *testing.T) {
	for _, ms := range []int{1000, 5000, 60000} {
		assert.NoError(t, validateCommandReviewTimeout(ms), ms)
	}
	for _, ms := range []int{0, 999, 60001} {
		assert.Error(t, validateCommandReviewTimeout(ms), ms)
	}
}
