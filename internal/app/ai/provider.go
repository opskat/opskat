package ai

import (
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/service/ai_provider_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// ExtraHeaderInput 前端提交的一条自定义请求头。
type ExtraHeaderInput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AIProviderInput 创建/更新 Provider 的入参。收成一个结构而不是继续排位置参数：
// 这个列表已经到 10 个，再加就没人能记住第 7 个 bool 是什么。
type AIProviderInput struct {
	Name             string             `json:"name"`
	Type             string             `json:"type"`
	APIBase          string             `json:"apiBase"`
	APIKey           string             `json:"apiKey"`
	Model            string             `json:"model"`
	MaxOutputTokens  int                `json:"maxOutputTokens"`
	ContextWindow    int                `json:"contextWindow"`
	ReasoningEnabled bool               `json:"reasoningEnabled"`
	ReasoningEffort  string             `json:"reasoningEffort"`
	ExtraHeaders     []ExtraHeaderInput `json:"extraHeaders"`
}

// reservedHeaderNames 由 OpsKat 自己写在请求上的头。用户配了同名的只会被静默丢弃，
// 与其让他以为生效，不如在保存时就拒掉。
var reservedHeaderNames = map[string]bool{
	"authorization":     true,
	"x-api-key":         true,
	"anthropic-version": true,
	"content-type":      true,
}

// normalizeExtraHeaders 校验并规整前端提交的自定义请求头。
// 名称为空视为用户点了"添加"又改主意，直接丢弃；值不做裁剪，有网关要求带空格的字面量。
func normalizeExtraHeaders(input []ExtraHeaderInput) ([]ai_provider_entity.ExtraHeader, error) {
	var out []ai_provider_entity.ExtraHeader
	seen := make(map[string]bool, len(input))
	for _, h := range input {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if reservedHeaderNames[key] {
			return nil, fmt.Errorf("请求头 %s 由 OpsKat 设置，不能覆盖", name)
		}
		if seen[key] {
			return nil, fmt.Errorf("请求头 %s 重复", name)
		}
		seen[key] = true
		out = append(out, ai_provider_entity.ExtraHeader{Name: name, Value: h.Value})
	}
	return out, nil
}

// AIProviderInfo 返回给前端的 Provider 信息
type AIProviderInfo struct {
	ID               int64              `json:"id"`
	Name             string             `json:"name"`
	Type             string             `json:"type"`
	APIBase          string             `json:"apiBase"`
	APIKey           string             `json:"apiKey"`
	Model            string             `json:"model"`
	MaxOutputTokens  int                `json:"maxOutputTokens"`
	ContextWindow    int                `json:"contextWindow"`
	ReasoningEnabled bool               `json:"reasoningEnabled"`
	ReasoningEffort  string             `json:"reasoningEffort"`
	IsActive         bool               `json:"isActive"`
	ExtraHeaders     []ExtraHeaderInput `json:"extraHeaders"`
}

func toProviderInfo(p *ai_provider_entity.AIProvider, apiKey string) AIProviderInfo {
	enabled, effort := normalizeProviderReasoningConfig(p.Type, p.ReasoningEnabled, p.ReasoningEffort)
	stored, err := p.GetExtraHeaders()
	if err != nil {
		// 列里是坏数据。界面展示成"没配过"会诱导用户重填一遍还是不生效，
		// 记一条日志让排查有迹可循。
		logger.Default().Warn("解析 Provider 自定义请求头失败",
			zap.Int64("provider_id", p.ID), zap.Error(err))
	}
	headers := make([]ExtraHeaderInput, 0, len(stored))
	for _, h := range stored {
		headers = append(headers, ExtraHeaderInput{Name: h.Name, Value: h.Value})
	}
	return AIProviderInfo{
		ID:               p.ID,
		Name:             p.Name,
		Type:             p.Type,
		APIBase:          p.APIBase,
		APIKey:           apiKey,
		Model:            p.Model,
		MaxOutputTokens:  p.MaxOutputTokens,
		ContextWindow:    p.ContextWindow,
		ReasoningEnabled: enabled,
		ReasoningEffort:  effort,
		IsActive:         p.IsActive,
		ExtraHeaders:     headers,
	}
}

func normalizeProviderReasoningConfig(providerType string, reasoningEnabled bool, reasoningEffort string) (bool, string) {
	switch providerType {
	case "openai", "anthropic":
	default:
		return false, ""
	}
	effort := strings.ToLower(strings.TrimSpace(reasoningEffort))
	if effort == "max" && providerType != "anthropic" {
		effort = "medium"
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true, effort
	case "none":
		return false, ""
	default:
		if reasoningEnabled {
			return true, "medium"
		}
		return false, ""
	}
}

// ListAIProviders 列出所有 Provider
func (a *AI) ListAIProviders() ([]AIProviderInfo, error) {
	list, err := ai_provider_svc.AIProvider().List(i18n.Ctx(a.ctx, a.lang.Lang()))
	if err != nil {
		return nil, err
	}
	result := make([]AIProviderInfo, 0, len(list))
	for _, p := range list {
		decrypted, err := ai_provider_svc.AIProvider().DecryptAPIKey(p)
		if err != nil {
			return nil, fmt.Errorf("解密 Provider API Key 失败 (id=%d): %w", p.ID, err)
		}
		result = append(result, toProviderInfo(p, decrypted))
	}
	return result, nil
}

// GetActiveAIProvider 获取当前激活的 Provider
func (a *AI) GetActiveAIProvider() (*AIProviderInfo, error) {
	p, err := ai_provider_svc.AIProvider().GetActive(i18n.Ctx(a.ctx, a.lang.Lang()))
	if err != nil {
		return nil, fmt.Errorf("获取激活 Provider 失败: %w", err)
	}
	if p == nil {
		return nil, nil
	}
	decrypted, err := ai_provider_svc.AIProvider().DecryptAPIKey(p)
	if err != nil {
		return nil, fmt.Errorf("解密 Provider API Key 失败 (id=%d): %w", p.ID, err)
	}
	info := toProviderInfo(p, decrypted)
	return &info, nil
}

// CreateAIProvider 创建新 Provider
func (a *AI) CreateAIProvider(in AIProviderInput) (*AIProviderInfo, error) {
	reasoningEnabled, reasoningEffort := normalizeProviderReasoningConfig(in.Type, in.ReasoningEnabled, in.ReasoningEffort)
	headers, err := normalizeExtraHeaders(in.ExtraHeaders)
	if err != nil {
		return nil, err
	}
	p := &ai_provider_entity.AIProvider{
		Name:             in.Name,
		Type:             in.Type,
		APIBase:          in.APIBase,
		Model:            in.Model,
		MaxOutputTokens:  in.MaxOutputTokens,
		ContextWindow:    in.ContextWindow,
		ReasoningEnabled: reasoningEnabled,
		ReasoningEffort:  reasoningEffort,
	}
	if err := p.SetExtraHeaders(headers); err != nil {
		return nil, err
	}
	if err := ai_provider_svc.AIProvider().Create(i18n.Ctx(a.ctx, a.lang.Lang()), p, in.APIKey); err != nil {
		return nil, fmt.Errorf("创建 Provider 失败: %w", err)
	}
	info := toProviderInfo(p, in.APIKey)
	return &info, nil
}

// UpdateAIProvider 更新 Provider
func (a *AI) UpdateAIProvider(id int64, in AIProviderInput) error {
	p, err := ai_provider_svc.AIProvider().Get(i18n.Ctx(a.ctx, a.lang.Lang()), id)
	if err != nil {
		return fmt.Errorf("provider 不存在: %w", err)
	}
	reasoningEnabled, reasoningEffort := normalizeProviderReasoningConfig(in.Type, in.ReasoningEnabled, in.ReasoningEffort)
	headers, err := normalizeExtraHeaders(in.ExtraHeaders)
	if err != nil {
		return err
	}
	p.Name = in.Name
	p.Type = in.Type
	p.APIBase = in.APIBase
	p.Model = in.Model
	p.MaxOutputTokens = in.MaxOutputTokens
	p.ContextWindow = in.ContextWindow
	p.ReasoningEnabled = reasoningEnabled
	p.ReasoningEffort = reasoningEffort
	if err := p.SetExtraHeaders(headers); err != nil {
		return err
	}
	if err := ai_provider_svc.AIProvider().Update(i18n.Ctx(a.ctx, a.lang.Lang()), p, in.APIKey); err != nil {
		return fmt.Errorf("更新 Provider 失败: %w", err)
	}

	if p.IsActive {
		return a.activateProvider(p)
	}
	return nil
}

// DeleteAIProvider 删除 Provider
func (a *AI) DeleteAIProvider(id int64) error {
	p, err := ai_provider_svc.AIProvider().Get(i18n.Ctx(a.ctx, a.lang.Lang()), id)
	if err != nil {
		return fmt.Errorf("provider 不存在: %w", err)
	}
	if p.IsActive {
		a.systemCfg = nil
		a.resetRunners()
	}
	return ai_provider_svc.AIProvider().Delete(i18n.Ctx(a.ctx, a.lang.Lang()), id)
}

// SetActiveAIProvider 切换激活 Provider 并创建 Agent
func (a *AI) SetActiveAIProvider(id int64) error {
	if err := ai_provider_svc.AIProvider().SetActive(i18n.Ctx(a.ctx, a.lang.Lang()), id); err != nil {
		return fmt.Errorf("激活 Provider 失败: %w", err)
	}
	p, err := ai_provider_svc.AIProvider().Get(i18n.Ctx(a.ctx, a.lang.Lang()), id)
	if err != nil {
		return err
	}
	return a.activateProvider(p)
}

// AIModelInfo 模型信息
type AIModelInfo struct {
	ID              string `json:"id"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	ContextWindow   int    `json:"contextWindow"`
}

// FetchAIModels 从 API 获取可用模型列表
func (a *AI) FetchAIModels(providerType, apiBase, apiKey string) ([]AIModelInfo, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API Key 不能为空")
	}

	models, err := runner.FetchModels(providerType, apiBase, apiKey)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表失败: %w", err)
	}

	result := make([]AIModelInfo, 0, len(models))
	for _, m := range models {
		info := AIModelInfo{ID: m.ID}
		if d := runner.GetModelDefaults(m.ID); d != nil {
			info.MaxOutputTokens = d.MaxOutputTokens
			info.ContextWindow = d.ContextWindow
		} else {
			info.MaxOutputTokens = runner.FallbackMaxOutputTokens
			info.ContextWindow = runner.FallbackContextWindow
		}
		result = append(result, info)
	}
	return result, nil
}

// GetModelDefaults 获取模型的默认参数，未知模型返回 fallback 默认值
func (a *AI) GetModelDefaults(model string) *AIModelInfo {
	d := runner.GetModelDefaults(model)
	if d != nil {
		return &AIModelInfo{
			ID:              model,
			MaxOutputTokens: d.MaxOutputTokens,
			ContextWindow:   d.ContextWindow,
		}
	}
	return &AIModelInfo{
		ID:              model,
		MaxOutputTokens: runner.FallbackMaxOutputTokens,
		ContextWindow:   runner.FallbackContextWindow,
	}
}
