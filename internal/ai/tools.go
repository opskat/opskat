package ai

// 预定义 tool schema（OpenAI function calling 格式）

// AssetTools 资产管理相关工具
func AssetTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "list_assets",
				Description: "列出资产，支持按类型和分组过滤",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"asset_type": map[string]any{
							"type":        "string",
							"description": "资产类型，如 ssh",
						},
						"group_id": map[string]any{
							"type":        "number",
							"description": "分组ID，0表示不过滤",
						},
					},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_asset",
				Description: "获取资产详细信息",
				Parameters: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "number",
							"description": "资产ID",
						},
					},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "run_command",
				Description: "在SSH资产上执行命令并返回输出",
				Parameters: map[string]any{
					"type":     "object",
					"required": []string{"asset_id", "command"},
					"properties": map[string]any{
						"asset_id": map[string]any{
							"type":        "number",
							"description": "SSH资产ID",
						},
						"command": map[string]any{
							"type":        "string",
							"description": "要执行的命令",
						},
						"password": map[string]any{
							"type":        "string",
							"description": "SSH密码（如需要）",
						},
					},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "add_asset",
				Description: "添加新资产",
				Parameters: map[string]any{
					"type":     "object",
					"required": []string{"name", "type", "host", "port", "username"},
					"properties": map[string]any{
						"name":        map[string]any{"type": "string", "description": "资产名称"},
						"type":        map[string]any{"type": "string", "description": "资产类型，目前支持 ssh"},
						"host":        map[string]any{"type": "string", "description": "主机地址"},
						"port":        map[string]any{"type": "number", "description": "端口号"},
						"username":    map[string]any{"type": "string", "description": "用户名"},
						"auth_type":   map[string]any{"type": "string", "description": "认证方式: password 或 key"},
						"group_id":    map[string]any{"type": "number", "description": "分组ID"},
						"description": map[string]any{"type": "string", "description": "描述"},
					},
				},
			},
		},
	}
}
