package tool

import (
	"context"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
)

func credentialTools() []tool.Tool {
	return []tool.Tool{
		&tool.RawTool{
			NameStr: "list_credentials",
			DescStr: "List safe managed credential and SSH Agent source metadata. Optional type filter: password, ssh_key, or ssh_agent. Results never contain passwords, private keys, passphrases, ciphertext, or Agent endpoint values.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"type": {Type: "string", Description: "Optional credential type filter: password, ssh_key, or ssh_agent."},
				},
			},
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleListCredentials(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
		&tool.RawTool{
			NameStr: "get_credential",
			DescStr: "Get safe credential detail and usage by typed ref. Use credential:<id> for managed passwords/SSH keys or agent-source:<id> for SSH Agent sources. Bare numeric IDs are rejected. No secret or Agent endpoint value is returned.",
			SchemaVal: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"ref": {Type: "string", Description: "Typed ref: credential:<id> or agent-source:<id>."},
				},
				Required: []string{"ref"},
			},
			Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
				out, err := handleGetCredential(ctx, in)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: out}}}, nil
			},
		},
	}
}
