package extension_settings_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/opskat/opskat/internal/model/entity/extension_settings_entity"
)

//go:generate mockgen -source=extension_settings.go -destination=mock_extension_settings_repo/extension_settings.go -package=mock_extension_settings_repo

// ExtensionSettingsRepo 读写扩展设置（每个扩展一行）。它与扩展的 KV（extension_data_repo）
// 是两张表：扩展经 KV 读写不到设置。
type ExtensionSettingsRepo interface {
	// Find 返回扩展已存的设置；没有存过时返回 (nil, nil)。
	Find(ctx context.Context, extName string) (*extension_settings_entity.ExtensionSettings, error)
	// Save 写入扩展的设置 JSON，覆盖旧值。
	Save(ctx context.Context, extName, value string) error
	// Delete 删除扩展的设置；没有时什么也不做。
	Delete(ctx context.Context, extName string) error
}

var defaultRepo ExtensionSettingsRepo

// ExtensionSettings 获取 ExtensionSettingsRepo 实例
func ExtensionSettings() ExtensionSettingsRepo {
	return defaultRepo
}

// RegisterExtensionSettings 注册 ExtensionSettingsRepo 实现
func RegisterExtensionSettings(r ExtensionSettingsRepo) {
	defaultRepo = r
}

type extensionSettingsRepo struct{}

// NewExtensionSettings 创建默认实现
func NewExtensionSettings() ExtensionSettingsRepo { return &extensionSettingsRepo{} }

func (r *extensionSettingsRepo) Find(ctx context.Context, extName string) (*extension_settings_entity.ExtensionSettings, error) {
	var row extension_settings_entity.ExtensionSettings
	err := db.Ctx(ctx).Where("extension_name = ?", extName).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *extensionSettingsRepo) Save(ctx context.Context, extName, value string) error {
	now := time.Now().Unix()
	row := extension_settings_entity.ExtensionSettings{
		ExtensionName: extName,
		Value:         value,
		Createtime:    now,
		Updatetime:    now,
	}
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "extension_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updatetime"}),
	}).Create(&row).Error
}

func (r *extensionSettingsRepo) Delete(ctx context.Context, extName string) error {
	return db.Ctx(ctx).Where("extension_name = ?", extName).Delete(&extension_settings_entity.ExtensionSettings{}).Error
}
