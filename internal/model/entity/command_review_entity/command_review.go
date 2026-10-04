package command_review_entity

// CommandReview 是一条模型审核结果的缓存。CacheKey 是服务地址、模型、题目版本、资产类型和
// 原始命令算出的哈希（见 command_review_svc 的 cacheKey）；不保存命令原文。
type CommandReview struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	CacheKey   string `gorm:"column:cache_key;type:varchar(64);not null;uniqueIndex:uq_command_reviews_cache_key"`
	Result     string `gorm:"column:result;type:text;not null"` // 审核结果 JSON
	Createtime int64  `gorm:"column:createtime"`
	Expiretime int64  `gorm:"column:expiretime;index:idx_command_reviews_expiretime"`
}

// TableName GORM 表名
func (CommandReview) TableName() string {
	return "command_reviews"
}
