package extension_state_entity

type ExtensionState struct {
	ID      int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name    string `gorm:"column:name;uniqueIndex:idx_ext_state_name"`
	Enabled bool   `gorm:"column:enabled;default:true"`
	// Source 是安装来源：store / local / dev。空表示这条记录写于来源字段存在之前。
	Source string `gorm:"column:source"`
	// PackageSHA256 是安装时已装 WASM 的摘要。加载审核器前重算并比对。
	PackageSHA256 string `gorm:"column:package_sha256"`
	Createtime    int64  `gorm:"column:createtime"`
	Updatetime    int64  `gorm:"column:updatetime"`
}

func (ExtensionState) TableName() string {
	return "extension_state"
}
