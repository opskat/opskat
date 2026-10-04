package extension_describe_entity

// ExtensionDescribe caches one extension's describe() answer.
//
// It is a cache, not a source of truth: WasmHash records which binary produced the
// descriptor, and an entry whose hash no longer matches the module on disk is
// simply ignored and overwritten on the next load.
type ExtensionDescribe struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string `gorm:"column:name;uniqueIndex:idx_ext_describe_name"`
	WasmHash   string `gorm:"column:wasm_hash"`
	Descriptor string `gorm:"column:descriptor"`
	Createtime int64  `gorm:"column:createtime"`
	Updatetime int64  `gorm:"column:updatetime"`
}

func (ExtensionDescribe) TableName() string {
	return "extension_describe"
}
