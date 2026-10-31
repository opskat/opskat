package extensiontest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An SDK registration mistake must reach the host as a load error that names the
// tool and the reason, not as a wasm trap with a stack trace.
func TestRegistrationMistakeFailsLoadNamingToolAndReason(t *testing.T) {
	err := LoadBadKindsFixtureError(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tool "bad_tool"`)
	assert.Contains(t, err.Error(), "HostOnly")
	assert.Contains(t, err.Error(), "PageOnly")
	assert.NotContains(t, err.Error(), "unreachable")
}
