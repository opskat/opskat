package host_key_repo

//go:generate mockgen -source=host_key.go -destination=mock_host_key_repo/host_key.go -package=mock_host_key_repo

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/host_key_entity"

	"github.com/cago-frame/cago/database/db"
)

// HostKeyRepo 主机密钥数据访问接口
type HostKeyRepo interface {
	FindByHostPortKeyType(ctx context.Context, host string, port int, keyType string) (*host_key_entity.HostKey, error)
	UpdateLastSeen(ctx context.Context, id int64, lastSeen int64) error
	Upsert(ctx context.Context, key *host_key_entity.HostKey) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context) ([]*host_key_entity.HostKey, error)
}

var instance HostKeyRepo

// RegisterHostKey 注册实现
func RegisterHostKey(repo HostKeyRepo) {
	instance = repo
}

// HostKey 获取全局实例
func HostKey() HostKeyRepo {
	return instance
}

// hostKeyRepo 默认实现
type hostKeyRepo struct{}

// NewHostKey 创建默认实现
func NewHostKey() HostKeyRepo {
	return &hostKeyRepo{}
}

func (r *hostKeyRepo) FindByHostPortKeyType(ctx context.Context, host string, port int, keyType string) (*host_key_entity.HostKey, error) {
	var key host_key_entity.HostKey
	result := db.Ctx(ctx).Where("host = ? AND port = ? AND key_type = ?", host, port, keyType).First(&key)
	if result.Error != nil {
		return nil, result.Error
	}
	return &key, nil
}

func (r *hostKeyRepo) UpdateLastSeen(ctx context.Context, id int64, lastSeen int64) error {
	return db.Ctx(ctx).Model(&host_key_entity.HostKey{}).Where("id = ?", id).Update("last_seen", lastSeen).Error
}

func (r *hostKeyRepo) Upsert(ctx context.Context, key *host_key_entity.HostKey) error {
	if key.ID > 0 {
		return db.Ctx(ctx).Save(key).Error
	}
	return db.Ctx(ctx).Create(key).Error
}

func (r *hostKeyRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&host_key_entity.HostKey{}, id).Error
}

func (r *hostKeyRepo) List(ctx context.Context) ([]*host_key_entity.HostKey, error) {
	var keys []*host_key_entity.HostKey
	if err := db.Ctx(ctx).Order("last_seen DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}
