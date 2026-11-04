package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePermissionMode(t *testing.T) {
	for _, m := range []string{PermissionModeInherit, PermissionModeDefault, PermissionModeAssisted, PermissionModeAutopilot} {
		assert.NoError(t, ValidatePermissionMode(m), m)
	}
	assert.Error(t, ValidatePermissionMode("allow_all"))
	assert.Error(t, ValidatePermissionMode("Autopilot"))
}
