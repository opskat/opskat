package command

import (
	"context"

	"github.com/opskat/opskat/internal/ai/tool"
)

func cmdListCredentials(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, credentialType string) int {
	params := map[string]any{}
	if credentialType != "" {
		params["type"] = credentialType
	}
	return callHandler(ctx, handlers, "list_credentials", params)
}

func cmdGetCredential(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, ref string) int {
	return callHandler(ctx, handlers, "get_credential", map[string]any{"ref": ref})
}
