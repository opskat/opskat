package extension

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// extToolCallCommand renders a page's (tool, args) call into the exec DSL text
// internal/extreg's parseCommand parses for every other caller of an extension
// tool (AI, opsctl). A page call's args always arrive as a JSON object rather
// than named flags, so the round trip through cmdline.Parse (the same parser
// parseCommand uses) is the contract this test protects — a hand-rolled quoting
// mistake here would silently corrupt args containing quotes or JSON's own
// punctuation before the gate ever sees them.
func TestExtToolCallCommandRoundTripsThroughCmdlineParse(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{name: "simple object", tool: "note_list", args: `{"prefix":"runbook"}`},
		{name: "embedded single quote", tool: "note_put", args: `{"key":"k1","content":"it's a note"}`},
		{name: "embedded double quotes and backslash", tool: "note_put", args: `{"key":"k1","content":"say \"hi\" \\ done"}`},
		{name: "empty args defaults to empty object", tool: "note_list", args: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := extToolCallCommand(tc.tool, json.RawMessage(tc.args))

			parsed, err := cmdline.Parse(command)
			require.NoError(t, err)
			require.Equal(t, tc.tool, parsed.Verb)

			want := tc.args
			if want == "" {
				want = "{}"
			}
			require.JSONEq(t, want, parsed.Flags["json"])
		})
	}
}
