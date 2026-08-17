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
			DescStr: "List the unified safe key-management inventory. Omit type for password, ssh_key, and ssh_agent; or filter by one kind. Every item has a typed ref (credential:<id> or agent-source:<id>), public identification metadata, and usage/status counts where applicable. Agent connectivity problems are represented by availability instead of exposing endpoint values. Never returns password/ciphertext, private key/passphrase, or Agent endpoint/signing material.",
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
			DescStr: "Get safe credential or SSH Agent source detail by typed ref. Use credential:<id> for managed passwords/SSH keys or agent-source:<id> for Agent sources; bare numeric IDs are ambiguous and rejected. Password/key detail includes referencing assets; SSH-key detail may include its public key. Agent detail includes persisted source metadata, availability, sanitized identity fingerprints/comments and usage, but never endpoint values or full Agent public keys. No password/ciphertext, private key/passphrase, signature, challenge, or challenge answer is returned.",
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
