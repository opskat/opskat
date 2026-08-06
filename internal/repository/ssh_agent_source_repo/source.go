// Package ssh_agent_source_repo 提供 SSH Agent 来源定义的持久化访问。
package ssh_agent_source_repo

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"

	"github.com/cago-frame/cago/database/db"
)

// SSHAgentSourceRepo SSH Agent 来源数据访问接口
type SSHAgentSourceRepo interface {
	Find(ctx context.Context, id int64) (*ssh_agent_source_entity.SSHAgentSource, error)
	List(ctx context.Context) ([]*ssh_agent_source_entity.SSHAgentSource, error)
	Create(ctx context.Context, src *ssh_agent_source_entity.SSHAgentSource) error
	Update(ctx context.Context, src *ssh_agent_source_entity.SSHAgentSource) error
	Delete(ctx context.Context, id int64) error
}

var instance SSHAgentSourceRepo

// RegisterSSHAgentSource 注册实现
func RegisterSSHAgentSource(repo SSHAgentSourceRepo) {
	instance = repo
}

// SSHAgentSource 获取全局实例
func SSHAgentSource() SSHAgentSourceRepo {
	return instance
}

// sshAgentSourceRepo 默认实现
type sshAgentSourceRepo struct{}

// New 创建默认实现
func New() SSHAgentSourceRepo {
	return &sshAgentSourceRepo{}
}

func (r *sshAgentSourceRepo) Find(ctx context.Context, id int64) (*ssh_agent_source_entity.SSHAgentSource, error) {
	var src ssh_agent_source_entity.SSHAgentSource
	if err := db.Ctx(ctx).Where("id = ?", id).First(&src).Error; err != nil {
		return nil, err
	}
	return &src, nil
}

func (r *sshAgentSourceRepo) List(ctx context.Context) ([]*ssh_agent_source_entity.SSHAgentSource, error) {
	var sources []*ssh_agent_source_entity.SSHAgentSource
	if err := db.Ctx(ctx).Order("createtime ASC, id ASC").Find(&sources).Error; err != nil {
		return nil, err
	}
	return sources, nil
}

func (r *sshAgentSourceRepo) Create(ctx context.Context, src *ssh_agent_source_entity.SSHAgentSource) error {
	return db.Ctx(ctx).Create(src).Error
}

func (r *sshAgentSourceRepo) Update(ctx context.Context, src *ssh_agent_source_entity.SSHAgentSource) error {
	return db.Ctx(ctx).Save(src).Error
}

func (r *sshAgentSourceRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&ssh_agent_source_entity.SSHAgentSource{}, id).Error
}
