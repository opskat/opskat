package extension

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func grantReferenceManifest() *Manifest {
	return &Manifest{
		Tools: []ToolDef{
			{Name: "note_put", PolicyAction: "write"},
			{Name: "note_get", PolicyActions: []string{"read", "list"}},
		},
		Policies: PoliciesDef{Type: "notebook", Actions: []string{"read", "list", "write"}},
	}
}

// An extension asset's grants and rules are written <action>[:<resource-glob>], not as
// commands, so the reference the model reads must name the action each tool requests
// and the actions a request_permission pattern may use.
func TestToolReferenceTellsTheModelTheGrantFormat(t *testing.T) {
	ref := grantReferenceManifest().ToolReference()

	// The grant format is its own section after the tools.
	grantsAt := strings.LastIndex(ref, "\n## ")
	put := ref[strings.Index(ref, "### note_put"):grantsAt]
	assert.Contains(t, put, "`write`", "each tool names the policy action it requests")
	get := ref[strings.Index(ref, "### note_get"):strings.Index(ref, "### note_put")]
	assert.Contains(t, get, "`read`")
	assert.Contains(t, get, "`list`")

	grants := ref[grantsAt:]
	require.Contains(t, grants, "request_permission", "the reference must say how to request a grant for this type")
	for _, action := range []string{"read", "list", "write"} {
		assert.Contains(t, grants, "`"+action+"`")
	}
	assert.Contains(t, grants, "<action>:<resource-glob>")
}

func TestToolReferenceOmitsGrantFormatWithoutPolicyActions(t *testing.T) {
	m := &Manifest{Tools: []ToolDef{{Name: "ping"}}}

	assert.NotContains(t, m.ToolReference(), "request_permission")
}
