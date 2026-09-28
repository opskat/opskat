// Package custom_type_repo 提供自定义类型的持久化访问。
package custom_type_repo

//go:generate mockgen -source=custom_type.go -destination=mock_custom_type_repo/custom_type.go -package=mock_custom_type_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
)

// CustomTypeRepo 自定义类型数据访问接口。未找到时 Find / FindBySlug 返回
// gorm.ErrRecordNotFound。
type CustomTypeRepo interface {
	List(ctx context.Context) ([]*custom_type_entity.CustomType, error)
	Find(ctx context.Context, id int64) (*custom_type_entity.CustomType, error)
	FindBySlug(ctx context.Context, slug string) (*custom_type_entity.CustomType, error)
	Create(ctx context.Context, ct *custom_type_entity.CustomType) error
	Update(ctx context.Context, ct *custom_type_entity.CustomType) error
	Delete(ctx context.Context, id int64) error
}

var defaultCustomType CustomTypeRepo

// CustomType 获取 CustomTypeRepo 实例
func CustomType() CustomTypeRepo { return defaultCustomType }

// RegisterCustomType 注册 CustomTypeRepo 实现
func RegisterCustomType(r CustomTypeRepo) { defaultCustomType = r }

type customTypeRepo struct{}

// New 创建默认实现
func New() CustomTypeRepo { return &customTypeRepo{} }

func (r *customTypeRepo) List(ctx context.Context) ([]*custom_type_entity.CustomType, error) {
	var list []*custom_type_entity.CustomType
	if err := db.Ctx(ctx).Order("createtime ASC, id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *customTypeRepo) Find(ctx context.Context, id int64) (*custom_type_entity.CustomType, error) {
	var ct custom_type_entity.CustomType
	if err := db.Ctx(ctx).Where("id = ?", id).First(&ct).Error; err != nil {
		return nil, err
	}
	return &ct, nil
}

func (r *customTypeRepo) FindBySlug(ctx context.Context, slug string) (*custom_type_entity.CustomType, error) {
	var ct custom_type_entity.CustomType
	if err := db.Ctx(ctx).Where("slug = ?", slug).First(&ct).Error; err != nil {
		return nil, err
	}
	return &ct, nil
}

func (r *customTypeRepo) Create(ctx context.Context, ct *custom_type_entity.CustomType) error {
	return db.Ctx(ctx).Create(ct).Error
}

func (r *customTypeRepo) Update(ctx context.Context, ct *custom_type_entity.CustomType) error {
	return db.Ctx(ctx).Save(ct).Error
}

func (r *customTypeRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&custom_type_entity.CustomType{}, id).Error
}
