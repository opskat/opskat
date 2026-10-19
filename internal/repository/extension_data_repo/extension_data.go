package extension_data_repo

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/model/entity/extension_data_entity"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxKVValueSize    = 1024 * 1024      // 1MB per value
	MaxKVPerExtension = 10 * 1024 * 1024 // 10MB total per extension
)

type ExtensionDataRepo interface {
	Get(ctx context.Context, extName, key string) ([]byte, error)
	Set(ctx context.Context, extName, key string, value []byte) error
	Delete(ctx context.Context, extName, key string) error
	DeleteAll(ctx context.Context, extName string) error
}

var defaultRepo ExtensionDataRepo

func ExtensionData() ExtensionDataRepo {
	return defaultRepo
}

func RegisterExtensionData(r ExtensionDataRepo) {
	defaultRepo = r
}

type extensionDataRepo struct{}

func NewExtensionData() ExtensionDataRepo {
	return &extensionDataRepo{}
}

func (r *extensionDataRepo) Get(ctx context.Context, extName, key string) ([]byte, error) {
	var row extension_data_entity.ExtensionData
	err := db.Ctx(ctx).Where("extension_name = ? AND key = ?", extName, key).First(&row).Error
	if err != nil {
		return nil, err
	}
	return row.Value, nil
}

func (r *extensionDataRepo) Set(ctx context.Context, extName, key string, value []byte) error {
	if len(value) > MaxKVValueSize {
		return fmt.Errorf("extension KV: value exceeds %d bytes (got %d)", MaxKVValueSize, len(value))
	}

	// Quota check and write share one transaction so that two processes (desktop app
	// and opsctl) writing the same extension cannot both pass the check. The
	// transaction begins immediately (see bootstrap.SQLiteDSN), which makes SQLite
	// serialise writers and lets the loser wait instead of failing.
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		// Total size of the other keys of this extension (the key being written is replaced).
		var totalSize int64
		err := tx.Model(&extension_data_entity.ExtensionData{}).
			Where("extension_name = ? AND key != ?", extName, key).
			Select("COALESCE(SUM(LENGTH(value)), 0)").
			Row().Scan(&totalSize)
		if err != nil {
			return fmt.Errorf("query KV total size: %w", err)
		}
		if totalSize+int64(len(value)) > MaxKVPerExtension {
			return fmt.Errorf("extension KV: extension %q quota exceeded (%d + %d > %d)",
				extName, totalSize, len(value), MaxKVPerExtension)
		}

		row := extension_data_entity.ExtensionData{
			ExtensionName: extName,
			Key:           key,
			Value:         value,
			Updatetime:    time.Now().Unix(),
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "extension_name"}, {Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updatetime"}),
		}).Create(&row).Error
	})
}

func (r *extensionDataRepo) Delete(ctx context.Context, extName, key string) error {
	return db.Ctx(ctx).Where("extension_name = ? AND key = ?", extName, key).Delete(&extension_data_entity.ExtensionData{}).Error
}

func (r *extensionDataRepo) DeleteAll(ctx context.Context, extName string) error {
	return db.Ctx(ctx).Where("extension_name = ?", extName).Delete(&extension_data_entity.ExtensionData{}).Error
}
