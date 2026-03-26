package app

import (
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/service/ai_provider_svc"
)

// AIProviderInfo 返回给前端的 Provider 信息（API Key 脱敏）
type AIProviderInfo struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	APIBase      string `json:"apiBase"`
	MaskedAPIKey string `json:"maskedApiKey"`
	Model        string `json:"model"`
	IsActive     bool   `json:"isActive"`
}

// ListAIProviders 列出所有 Provider（API Key 脱敏）
func (a *App) ListAIProviders() ([]AIProviderInfo, error) {
	list, err := ai_provider_svc.AIProvider().List(a.langCtx())
	if err != nil {
		return nil, err
	}
	result := make([]AIProviderInfo, 0, len(list))
	for _, p := range list {
		decrypted, _ := ai_provider_svc.AIProvider().DecryptAPIKey(p)
		result = append(result, AIProviderInfo{
			ID:           p.ID,
			Name:         p.Name,
			Type:         p.Type,
			APIBase:      p.APIBase,
			MaskedAPIKey: maskAPIKey(decrypted),
			Model:        p.Model,
			IsActive:     p.IsActive,
		})
	}
	return result, nil
}

// GetActiveAIProvider 获取当前激活的 Provider
func (a *App) GetActiveAIProvider() (*AIProviderInfo, error) {
	p, err := ai_provider_svc.AIProvider().GetActive(a.langCtx())
	if err != nil {
		return nil, nil // 无激活 provider 时返回 nil
	}
	decrypted, _ := ai_provider_svc.AIProvider().DecryptAPIKey(p)
	return &AIProviderInfo{
		ID:           p.ID,
		Name:         p.Name,
		Type:         p.Type,
		APIBase:      p.APIBase,
		MaskedAPIKey: maskAPIKey(decrypted),
		Model:        p.Model,
		IsActive:     p.IsActive,
	}, nil
}

// CreateAIProvider 创建新 Provider
func (a *App) CreateAIProvider(name, providerType, apiBase, apiKey, model string) (*AIProviderInfo, error) {
	p := &ai_provider_entity.AIProvider{
		Name:    name,
		Type:    providerType,
		APIBase: apiBase,
		Model:   model,
	}
	if err := ai_provider_svc.AIProvider().Create(a.langCtx(), p, apiKey); err != nil {
		return nil, fmt.Errorf("创建 Provider 失败: %w", err)
	}
	return &AIProviderInfo{
		ID:           p.ID,
		Name:         p.Name,
		Type:         p.Type,
		APIBase:      p.APIBase,
		MaskedAPIKey: maskAPIKey(apiKey),
		Model:        p.Model,
		IsActive:     p.IsActive,
	}, nil
}

// UpdateAIProvider 更新 Provider
func (a *App) UpdateAIProvider(id int64, name, providerType, apiBase, apiKey, model string) error {
	p, err := ai_provider_svc.AIProvider().Get(a.langCtx(), id)
	if err != nil {
		return fmt.Errorf("Provider 不存在: %w", err)
	}
	p.Name = name
	p.Type = providerType
	p.APIBase = apiBase
	p.Model = model
	if err := ai_provider_svc.AIProvider().Update(a.langCtx(), p, apiKey); err != nil {
		return fmt.Errorf("更新 Provider 失败: %w", err)
	}

	// 如果更新的是激活的 Provider，重新加载
	if p.IsActive {
		return a.activateProvider(p)
	}
	return nil
}

// DeleteAIProvider 删除 Provider
func (a *App) DeleteAIProvider(id int64) error {
	p, err := ai_provider_svc.AIProvider().Get(a.langCtx(), id)
	if err != nil {
		return fmt.Errorf("Provider 不存在: %w", err)
	}
	if p.IsActive {
		a.aiAgent = nil
	}
	return ai_provider_svc.AIProvider().Delete(a.langCtx(), id)
}

// SetActiveAIProvider 切换激活 Provider 并创建 Agent
func (a *App) SetActiveAIProvider(id int64) error {
	if err := ai_provider_svc.AIProvider().SetActive(a.langCtx(), id); err != nil {
		return fmt.Errorf("激活 Provider 失败: %w", err)
	}
	p, err := ai_provider_svc.AIProvider().Get(a.langCtx(), id)
	if err != nil {
		return err
	}
	return a.activateProvider(p)
}
