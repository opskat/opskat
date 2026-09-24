package ai_provider_entity

import "encoding/json"

// AIProvider AI Provider 配置
type AIProvider struct {
	ID               int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name             string `gorm:"column:name;type:varchar(100);not null" json:"name"`
	Type             string `gorm:"column:type;type:varchar(50);not null" json:"type"` // "openai" | "anthropic"
	APIBase          string `gorm:"column:api_base;type:varchar(500);not null" json:"apiBase"`
	APIKey           string `gorm:"column:api_key;type:text" json:"-"` // 加密存储，JSON 忽略
	Model            string `gorm:"column:model;type:varchar(100)" json:"model"`
	MaxOutputTokens  int    `gorm:"column:max_output_tokens;default:0" json:"maxOutputTokens"` // 0 表示使用默认值
	ContextWindow    int    `gorm:"column:context_window;default:0" json:"contextWindow"`      // 0 表示使用默认值
	ReasoningEnabled bool   `gorm:"column:reasoning_enabled;default:false" json:"reasoningEnabled"`
	ReasoningEffort  string `gorm:"column:reasoning_effort;type:varchar(20);default:''" json:"reasoningEffort"` // OpenAI/Anthropic 共用：low/medium/high/xhigh/max（max 仅 Anthropic）
	ExtraHeaders     string `gorm:"column:extra_headers;type:text" json:"-"`                                    // []ExtraHeader 的 JSON，经 Get/SetExtraHeaders 存取
	IsActive         bool   `gorm:"column:is_active;default:false" json:"isActive"`
	Createtime       int64  `gorm:"column:createtime" json:"createtime"`
	Updatetime       int64  `gorm:"column:updatetime" json:"updatetime"`
}

func (AIProvider) TableName() string {
	return "ai_providers"
}

// ExtraHeader 附加到该 Provider 每个出站请求上的自定义 HTTP 头。
// 用切片而非 map：用户填写的顺序要在界面上原样回显。
type ExtraHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GetExtraHeaders 解析 ExtraHeaders 列。未配置时返回空集合；列里是坏数据时返回错误，
// 不当成"无配置"——那会让一个配置好的网关静默退回 400。
func (p *AIProvider) GetExtraHeaders() ([]ExtraHeader, error) {
	if p.ExtraHeaders == "" {
		return nil, nil
	}
	var headers []ExtraHeader
	if err := json.Unmarshal([]byte(p.ExtraHeaders), &headers); err != nil {
		return nil, err
	}
	return headers, nil
}

// SetExtraHeaders 写回 ExtraHeaders 列。空集合写成空串而不是 "null"，
// 让"没配过"在库里只有一种表示。
func (p *AIProvider) SetExtraHeaders(headers []ExtraHeader) error {
	if len(headers) == 0 {
		p.ExtraHeaders = ""
		return nil
	}
	encoded, err := json.Marshal(headers)
	if err != nil {
		return err
	}
	p.ExtraHeaders = string(encoded)
	return nil
}
