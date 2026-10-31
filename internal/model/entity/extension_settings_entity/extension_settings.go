package extension_settings_entity

// ExtensionSettings 是一个扩展的设置：Value 是设置 JSON，按扩展声明的 configSchema 校验后
// 保存，format:"password" 字段为密文（credential_svc 加密）。每个扩展至多一行。
type ExtensionSettings struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ExtensionName string `gorm:"column:extension_name;type:varchar(255);not null;uniqueIndex:uq_extension_settings_extension_name"`
	Value         string `gorm:"column:value;type:text;not null"`
	Createtime    int64  `gorm:"column:createtime"`
	Updatetime    int64  `gorm:"column:updatetime"`
}

// TableName GORM 表名
func (ExtensionSettings) TableName() string {
	return "extension_settings"
}
