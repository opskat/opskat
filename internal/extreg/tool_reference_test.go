package extreg

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
)

func grantReferenceManifest() *extension.Manifest {
	return &extension.Manifest{
		Tools: []extension.ToolDef{
			{Name: "note_put", PolicyAction: "write"},
			{Name: "note_get", PolicyActions: []string{"read", "list"}},
		},
		Policies: extension.PoliciesDef{Type: "notebook", Actions: []string{"read", "list", "write"}},
	}
}

// An extension asset's grants and rules are written <action>[:<resource-glob>], not as
// commands, so the reference the model reads must name the action each tool requests
// and the actions a request_permission pattern may use.
func TestToolReferenceTellsTheModelTheGrantFormat(t *testing.T) {
	ref := toolReference(grantReferenceManifest())

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
	m := &extension.Manifest{Tools: []extension.ToolDef{{Name: "ping", PolicyAction: "read"}}}

	assert.NotContains(t, toolReference(m), "request_permission")
}

// The `-file` form is only understood by opsctl: the parameter table must say so, or
// a model would try it through exec and get an unknown-flag error.
func TestToolReferenceMarksLargeParams(t *testing.T) {
	m := &extension.Manifest{
		Tools: []extension.ToolDef{{
			Name: "bulk", PolicyAction: "write", Large: []string{"body"},
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"body":  map[string]any{"type": "string"},
				"index": map[string]any{"type": "string"},
			}},
		}},
		Policies: extension.PoliciesDef{Type: "x", Actions: []string{"write"}},
	}
	ref := toolReference(m)
	assert.Contains(t, ref, "`--body-file`")
	assert.Contains(t, ref, "opsctl only")
	assert.NotContains(t, ref, "`--index-file`")
}

// A page-only tool cannot be called through exec, so the model must not be told
// it exists.
func TestToolReferenceLeavesOutPageOnlyTools(t *testing.T) {
	m := &extension.Manifest{
		Tools: []extension.ToolDef{
			{Name: "ping", PolicyAction: "read"},
			{Name: "ui_notes", PageOnly: true},
		},
		Policies: extension.PoliciesDef{Type: "x", Actions: []string{"read"}},
	}
	ref := toolReference(m)
	assert.Contains(t, ref, "### ping")
	assert.NotContains(t, ref, "ui_notes")

	onlyPages := &extension.Manifest{Tools: []extension.ToolDef{{Name: "ui_notes", PageOnly: true}}}
	assert.Empty(t, toolReference(onlyPages))
}
