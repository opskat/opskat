package extreg

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// A tool is exactly one of policy / page-only / host-only; host-only follows the
// policy tool's timeout rules and needs no asset type.
func TestHostOnlyToolDeclaration(t *testing.T) {
	const params = `"parameters":{"type":"object","properties":{}}`
	bare := func(entries ...string) error {
		return check(t, describeJSON(`"tools":[`+strings.Join(entries, ",")+`]`))
	}
	refused := func(entry string, wants ...string) {
		err := bare(entry)
		require.Error(t, err)
		for _, w := range wants {
			assert.Contains(t, err.Error(), w)
		}
	}
	t.Run("host-only alone needs no asset type nor policy type", func(t *testing.T) {
		require.NoError(t, bare(`{"name":"review","hostOnly":true,`+params+`}`))
	})
	t.Run("host-only with a page-only or a policy declaration is refused, naming the tool", func(t *testing.T) {
		refused(`{"name":"x","hostOnly":true,"pageOnly":true,`+params+`}`, "tools[x]", "hostOnly")
		refused(`{"name":"x","hostOnly":true,"policyAction":"read",`+params+`}`, "tools[x]", "hostOnly")
		refused(`{"name":"x","hostOnly":true,"policyActions":["read"],`+params+`}`, "tools[x]", "hostOnly")
	})
	t.Run("a host-only tool cannot declare noTimeout and keeps the 10 minute ceiling", func(t *testing.T) {
		refused(`{"name":"x","hostOnly":true,"noTimeout":true,`+params+`}`, "tools[x]", "noTimeout")
		require.NoError(t, bare(`{"name":"x","hostOnly":true,"timeoutMs":600000,`+params+`}`))
		refused(`{"name":"x","hostOnly":true,"timeoutMs":600001,`+params+`}`, "tools[x]", "timeoutMs")
	})
}
