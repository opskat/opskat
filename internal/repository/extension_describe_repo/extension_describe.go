package extension_describe_repo

import (
	"context"
	"errors"
	"time"

	"github.com/opskat/opskat/internal/model/entity/extension_describe_entity"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// Save upserts on name in one statement. A find-then-create would let two loads of
// the same extension race into the unique index, and the loser's answer would be lost.
func (r *extensionDescribeRepo) Save(ctx context.Context, row *extension_describe_entity.ExtensionDescribe) error {
	now := time.Now().Unix()
	row.Createtime = now
	row.Updatetime = now
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"wasm_hash", "descriptor", "updatetime"}),
	}).Create(row).Error
}

func (r *extensionDescribeRepo) Delete(ctx context.Context, name string) error {
	return db.Ctx(ctx).Where("name = ?", name).Delete(&extension_describe_entity.ExtensionDescribe{}).Error
}
