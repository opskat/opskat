package extreg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// ToolTimeout is the timeout every caller of a tool — AI exec, opsctl, the
// extension's page — hands the runtime in extension.ToolCall: none for a
// page-only tool declaring noTimeout, its declared timeout otherwise, the
// runtime's default (0) when it declares nothing.
func TestToolTimeout(t *testing.T) {
	m := &Manifest{Manifest: &extension.Manifest{}, Tools: []extwire.Tool{
		{Name: "stream", PageOnly: true, NoTimeout: true},
		{Name: "spin_short", PolicyAction: "read", TimeoutMs: 300},
		{Name: "echo", PolicyAction: "read"},
		{Name: "page", PageOnly: true},
	}}
	assert.Less(t, ToolTimeout(m, "stream"), time.Duration(0))
	assert.Equal(t, 300*time.Millisecond, ToolTimeout(m, "spin_short"))
	assert.Equal(t, time.Duration(0), ToolTimeout(m, "echo"))
	assert.Equal(t, time.Duration(0), ToolTimeout(m, "page"))
}
