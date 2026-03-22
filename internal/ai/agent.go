package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"ops-cat/internal/model/entity/asset_entity"
	"ops-cat/internal/service/asset_svc"
)

// ToolExecutor 执行 tool 调用的接口
type ToolExecutor interface {
	Execute(ctx context.Context, name string, args string) (string, error)
}

// Agent AI 代理，管理对话循环和 tool 调度
type Agent struct {
	provider Provider
	executor ToolExecutor
	tools    []Tool
}

// NewAgent 创建 Agent
func NewAgent(provider Provider, executor ToolExecutor) *Agent {
	return &Agent{
		provider: provider,
		executor: executor,
		tools:    AssetTools(),
	}
}

// Chat 发起对话，处理 tool 调用循环，通过回调流式返回内容
func (a *Agent) Chat(ctx context.Context, messages []Message, onEvent func(StreamEvent)) error {
	const maxRounds = 10 // 防止无限循环

	for round := 0; round < maxRounds; round++ {
		ch, err := a.provider.Chat(ctx, messages, a.tools)
		if err != nil {
			return fmt.Errorf("provider chat 失败: %w", err)
		}

		var contentBuf string
		var toolCalls []ToolCall
		hasToolCall := false

		for event := range ch {
			switch event.Type {
			case "content":
				contentBuf += event.Content
				onEvent(event)
			case "tool_call":
				toolCalls = event.ToolCalls
				hasToolCall = true
				onEvent(event)
			case "error":
				onEvent(event)
				return fmt.Errorf("provider 错误: %s", event.Error)
			case "done":
				// 不立即转发 done，可能还有 tool 调用
			}
		}

		// 没有 tool 调用，对话结束
		if !hasToolCall {
			onEvent(StreamEvent{Type: "done"})
			return nil
		}

		// 将 assistant 的回复（含 tool_calls）加入消息
		assistantMsg := Message{
			Role:      RoleAssistant,
			Content:   contentBuf,
			ToolCalls: toolCalls,
		}
		messages = append(messages, assistantMsg)

		// 执行每个 tool 调用
		for _, tc := range toolCalls {
			result, err := a.executor.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("工具执行错误: %s", err.Error())
			}
			messages = append(messages, Message{
				Role:       RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
		// 继续下一轮对话
	}

	onEvent(StreamEvent{Type: "done"})
	return nil
}

// DefaultToolExecutor 默认工具执行器
type DefaultToolExecutor struct{}

func NewDefaultToolExecutor() *DefaultToolExecutor {
	return &DefaultToolExecutor{}
}

func (e *DefaultToolExecutor) Execute(ctx context.Context, name string, argsJSON string) (string, error) {
	switch name {
	case "list_assets":
		return e.listAssets(ctx, argsJSON)
	case "get_asset":
		return e.getAsset(ctx, argsJSON)
	case "add_asset":
		return e.addAsset(ctx, argsJSON)
	case "run_command":
		return e.runCommand(ctx, argsJSON)
	default:
		return "", fmt.Errorf("未知工具: %s", name)
	}
}

func (e *DefaultToolExecutor) listAssets(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AssetType string `json:"asset_type"`
		GroupID   int64  `json:"group_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	assets, err := asset_svc.Asset().List(ctx, args.AssetType, args.GroupID)
	if err != nil {
		return "", err
	}
	result, _ := json.Marshal(assets)
	return string(result), nil
}

func (e *DefaultToolExecutor) getAsset(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	asset, err := asset_svc.Asset().Get(ctx, args.ID)
	if err != nil {
		return "", err
	}
	result, _ := json.Marshal(asset)
	return string(result), nil
}

func (e *DefaultToolExecutor) addAsset(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		AuthType    string `json:"auth_type"`
		GroupID     int64  `json:"group_id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	asset := &asset_entity.Asset{
		Name:        args.Name,
		Type:        args.Type,
		GroupID:     args.GroupID,
		Description: args.Description,
	}
	if args.Type == "ssh" {
		_ = asset.SetSSHConfig(&asset_entity.SSHConfig{
			Host:     args.Host,
			Port:     args.Port,
			Username: args.Username,
			AuthType: args.AuthType,
		})
	}
	if err := asset_svc.Asset().Create(ctx, asset); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"id":%d,"message":"资产创建成功"}`, asset.ID), nil
}

func (e *DefaultToolExecutor) runCommand(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AssetID  int64  `json:"asset_id"`
		Command  string `json:"command"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}

	// 获取资产
	asset, err := asset_svc.Asset().Get(ctx, args.AssetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsSSH() {
		return "", fmt.Errorf("资产不是SSH类型")
	}
	sshCfg, err := asset.GetSSHConfig()
	if err != nil {
		return "", err
	}

	// 执行一次性 SSH 命令
	output, err := executeSSHCommand(sshCfg, args.Password, args.Command)
	if err != nil {
		return "", err
	}
	return output, nil
}
