package extension_describe_repo

import (
	"context"
	"errors"
	"time"

	"github.com/opskat/opskat/internal/model/entity/extension_describe_entity"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
)

// ExtensionDescribeRepo stores the describe() answer of each installed extension.
// Find returns (nil, nil) when nothing is cached for that name — an absent cache
// entry is an ordinary outcome, not a failure.
type ExtensionDescribeRepo interface {
	Find(ctx context.Context, name string) (*extension_describe_entity.ExtensionDescribe, error)
	Save(ctx context.Context, row *extension_describe_entity.ExtensionDescribe) error
	Delete(ctx context.Context, name string) error
}

var defaultRepo ExtensionDescribeRepo

func ExtensionDescribe() ExtensionDescribeRepo {
	return defaultRepo
}

func RegisterExtensionDescribe(r ExtensionDescribeRepo) {
	defaultRepo = r
}

type extensionDescribeRepo struct{}

func NewExtensionDescribe() ExtensionDescribeRepo {
	return &extensionDescribeRepo{}
}

func (r *extensionDescribeRepo) Find(ctx context.Context, name string) (*extension_describe_entity.ExtensionDescribe, error) {
	var row extension_describe_entity.ExtensionDescribe
	err := db.Ctx(ctx).Where("name = ?", name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *extensionDescribeRepo) Save(ctx context.Context, row *extension_describe_entity.ExtensionDescribe) error {
	now := time.Now().Unix()
	row.Updatetime = now
	existing, err := r.Find(ctx, row.Name)
	if err != nil {
		return err
	}
	if existing == nil {
		row.Createtime = now
		return db.Ctx(ctx).Create(row).Error
	}
	existing.WasmHash = row.WasmHash
	existing.Descriptor = row.Descriptor
	existing.Updatetime = now
	return db.Ctx(ctx).Save(existing).Error
}

func (r *extensionDescribeRepo) Delete(ctx context.Context, name string) error {
	return db.Ctx(ctx).Where("name = ?", name).Delete(&extension_describe_entity.ExtensionDescribe{}).Error
}
