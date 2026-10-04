package extreg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
)

// registerBulkExtension registers acme with one extra tool whose `body` parameter
// is file-readable.
func registerBulkExtension(t *testing.T) *extension.Manifest {
	t.Helper()
	m := testManifest()
	m.Tools = append(m.Tools, extension.ToolDef{
		Name: "bulk",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"body":  map[string]any{"type": "string"},
				"index": map[string]any{"type": "string"},
			},
		},
		PolicyAction: "object.write",
		FileParams:   []string{"body"},
	}, extension.ToolDef{
		Name: "pair",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "string"},
				"b": map[string]any{"type": "string"},
			},
		},
		PolicyAction: "object.write",
		FileParams:   []string{"a", "b"},
	})
	localized := m.Localized(func(k string) string { return k })
	require.NoError(t, register(loaded{name: m.Name, manifest: m, plugin: &fakePlugin{}},
		helpDocument("", m.Name, localized), "acme skill"))
	t.Cleanup(func() { Unregister(m.Name) })
	return m
}

func writeTemp(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.txt")
	require.NoError(t, os.WriteFile(p, content, 0o600))
	return p
}

// argsOf parses the command the way the desktop does, so "equivalent to inline" is
// judged on what the tool would receive.
func argsOf(t *testing.T, m *extension.Manifest, command string) map[string]any {
	t.Helper()
	_, raw, err := parseCommand(m, command)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	return got
}

func TestExpandFileFlagsIsEquivalentToInline(t *testing.T) {
	m := registerBulkExtension(t)
	content := "{\"index\":{}}\n{\"a\": \"it's --x  \\\"q\\\"\"}\n"
	path := writeTemp(t, []byte(content))

	t.Run("path", func(t *testing.T) {
		cmd, err := ExpandFileFlags("acme", "bulk --index=logs --body-file "+path, strings.NewReader(""))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"index": "logs", "body": content}, argsOf(t, m, cmd))
	})
	t.Run("stdin", func(t *testing.T) {
		cmd, err := ExpandFileFlags("acme", "bulk --body-file - --index=logs", strings.NewReader(content))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"index": "logs", "body": content}, argsOf(t, m, cmd))
	})
	t.Run("a command without file flags is returned untouched", func(t *testing.T) {
		in := "bulk  --index=logs"
		cmd, err := ExpandFileFlags("acme", in, strings.NewReader(""))
		require.NoError(t, err)
		assert.Equal(t, in, cmd)
	})
	t.Run("content that looks like a boolean or a flag survives", func(t *testing.T) {
		for _, c := range []string{"true", "--index=x", ""} {
			cmd, err := ExpandFileFlags("acme", "bulk --body-file "+writeTemp(t, []byte(c)), strings.NewReader(""))
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"body": c}, argsOf(t, m, cmd), "content %q", c)
		}
	})
}

func TestExpandFileFlagsRefusals(t *testing.T) {
	registerBulkExtension(t)
	ok := writeTemp(t, []byte("x"))

	cases := map[string]struct {
		command string
		stdin   string
		want    string
	}{
		"both forms":       {"bulk --body=x --body-file " + ok, "", "--body"},
		"missing file":     {"bulk --body-file /nonexistent/nope", "", "nope"},
		"over 16 MiB":      {"bulk --body-file -", strings.Repeat("a", MaxFileParamBytes+1), "16 MiB"},
		"invalid UTF-8":    {"bulk --body-file -", "ok\xff\xfe", "UTF-8"},
		"stdin used twice": {"pair --a-file - --b-file -", "x", "stdin"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cmd, err := ExpandFileFlags("acme", c.command, strings.NewReader(c.stdin))
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			assert.Empty(t, cmd)
		})
	}

	t.Run("exactly 16 MiB is accepted", func(t *testing.T) {
		_, err := ExpandFileFlags("acme", "bulk --body-file -", strings.NewReader(strings.Repeat("a", MaxFileParamBytes)))
		require.NoError(t, err)
	})
	t.Run("a directory is not readable content", func(t *testing.T) {
		_, err := ExpandFileFlags("acme", "bulk --body-file "+t.TempDir(), strings.NewReader(""))
		require.Error(t, err)
	})
}

// The AI's exec and the extension page parse with parseCommand, which must keep
// treating `-file` as an unknown flag even for a file-readable parameter.
func TestParseCommandRejectsFileFlag(t *testing.T) {
	m := registerBulkExtension(t)
	_, _, err := parseCommand(m, "bulk --body-file=/etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no parameter "body-file"`)
}

func TestExpandFileFlagsOnlyForFileParamsOfTheTool(t *testing.T) {
	registerBulkExtension(t)
	// index is not file-readable: its -file form is not expanded (the desktop then
	// rejects it as an unknown flag).
	cmd, err := ExpandFileFlags("acme", "bulk --index-file=x", strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, "bulk --index-file=x", cmd)
}
