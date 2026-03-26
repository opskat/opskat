package ai_provider_entity

// AIProvider AI Provider 配置
type AIProvider struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name       string `gorm:"column:name;type:varchar(100);not null" json:"name"`
	Type       string `gorm:"column:type;type:varchar(50);not null" json:"type"` // "openai" | "anthropic"
	APIBase    string `gorm:"column:api_base;type:varchar(500);not null" json:"apiBase"`
	APIKey     string `gorm:"column:api_key;type:text" json:"-"` // 加密存储，JSON 忽略
	Model      string `gorm:"column:model;type:varchar(100)" json:"model"`
	IsActive   bool   `gorm:"column:is_active;default:false" json:"isActive"`
	Createtime int64  `gorm:"column:createtime" json:"createtime"`
	Updatetime int64  `gorm:"column:updatetime" json:"updatetime"`
}

func (AIProvider) TableName() string {
	return "ai_providers"
}
