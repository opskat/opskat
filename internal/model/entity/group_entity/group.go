package group_entity

import "errors"

// Group 资产分组实体
type Group struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string `gorm:"column:name;type:varchar(255);not null"`
	ParentID   int64  `gorm:"column:parent_id;index"`
	SortOrder  int    `gorm:"column:sort_order;default:0"`
	Createtime int64  `gorm:"column:createtime"`
	Updatetime int64  `gorm:"column:updatetime"`
}

// TableName GORM表名
func (Group) TableName() string {
	return "groups"
}

// Validate 校验分组
func (g *Group) Validate() error {
	if g.Name == "" {
		return errors.New("分组名称不能为空")
	}
	return nil
}

// IsRoot 是否为顶层分组
func (g *Group) IsRoot() bool {
	return g.ParentID == 0
}
