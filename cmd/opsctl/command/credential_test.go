package command

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentialCommandsRouteTypedReadOnlyHandlers(t *testing.T) {
	calls := map[string]map[string]any{}
	handlers := map[string]tool.ToolHandlerFunc{
		"list_credentials": func(_ context.Context, args map[string]any) (string, error) {
			calls["list_credentials"] = args
			return `[]`, nil
		},
		"get_credential": func(_ context.Context, args map[string]any) (string, error) {
			calls["get_credential"] = args
			return `{}`, nil
		},
	}

	assert.Equal(t, 0, cmdList(context.Background(), handlers, []string{"credentials", "--type", "ssh_agent"}))
	require.Contains(t, calls, "list_credentials")
	assert.Equal(t, "ssh_agent", calls["list_credentials"]["type"])

	assert.Equal(t, 0, cmdGet(context.Background(), handlers, []string{"credential", "agent-source:7"}))
	require.Contains(t, calls, "get_credential")
	assert.Equal(t, "agent-source:7", calls["get_credential"]["ref"])
}

func TestCredentialGetPreservesTypedRefValidationInSharedHandler(t *testing.T) {
	called := false
	handlers := map[string]tool.ToolHandlerFunc{
		"get_credential": func(_ context.Context, args map[string]any) (string, error) {
			called = true
			assert.Equal(t, "7", args["ref"])
			return "", assert.AnError
		},
	}

	assert.Equal(t, 1, cmdGet(context.Background(), handlers, []string{"credential", "7"}))
	assert.True(t, called)
}
