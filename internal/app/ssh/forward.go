package ssh

import (
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/forward_entity"
	"github.com/opskat/opskat/internal/repository/forward_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// CreateForwardConfig 创建转发配置
func (s *SSH) CreateForwardConfig(name string, assetID int64, rules []forward_entity.ForwardRule) (*forward_entity.ForwardConfig, error) {
	ctx := i18n.Ctx(s.ctx, s.lang.Lang())
	now := time.Now().Unix()
	config := &forward_entity.ForwardConfig{
		Name: name, AssetID: assetID,
		Createtime: now, Updatetime: now,
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := forward_repo.Forward().CreateConfig(ctx, config); err != nil {
		return nil, err
	}
	// 写入规则
	rulesPtrs := make([]*forward_entity.ForwardRule, len(rules))
	for i := range rules {
		rulesPtrs[i] = &rules[i]
	}
	if err := forward_repo.Forward().ReplaceRules(ctx, config.ID, rulesPtrs); err != nil {
		return nil, err
	}
	return config, nil
}

// UpdateForwardConfig 更新转发配置（如果正在运行，先停止再更新再启动）
func (s *SSH) UpdateForwardConfig(id int64, name string, assetID int64, rules []forward_entity.ForwardRule) (*forward_entity.ForwardConfig, error) {
	ctx := i18n.Ctx(s.ctx, s.lang.Lang())
	config, err := forward_repo.Forward().FindConfig(ctx, id)
	if err != nil {
		return nil, err
	}

	wasRunning := s.forwardManager.IsConfigRunning(id)
	if wasRunning {
		s.forwardManager.StopConfig(id)
	}

	config.Name = name
	config.AssetID = assetID
	config.Updatetime = time.Now().Unix()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := forward_repo.Forward().UpdateConfig(ctx, config); err != nil {
		return nil, err
	}
	rulesPtrs := make([]*forward_entity.ForwardRule, len(rules))
	for i := range rules {
		rulesPtrs[i] = &rules[i]
	}
	if err := forward_repo.Forward().ReplaceRules(ctx, config.ID, rulesPtrs); err != nil {
		return nil, err
	}

	if wasRunning {
		if err := s.forwardManager.StartConfig(ctx, id); err != nil {
			logger.Default().Error("restart forward config after update", zap.Int64("id", id), zap.Error(err))
		}
	}

	return config, nil
}

// DeleteForwardConfig 删除转发配置
func (s *SSH) DeleteForwardConfig(id int64) error {
	s.forwardManager.StopConfig(id)
	ctx := i18n.Ctx(s.ctx, s.lang.Lang())
	if err := forward_repo.Forward().DeleteRulesByConfigID(ctx, id); err != nil {
		return err
	}
	return forward_repo.Forward().DeleteConfig(ctx, id)
}

// ListForwardConfigs 列出所有转发配置（含规则和运行状态）
func (s *SSH) ListForwardConfigs() ([]ForwardConfigWithStatus, error) {
	ctx := i18n.Ctx(s.ctx, s.lang.Lang())
	configs, err := forward_repo.Forward().ListConfigs(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]ForwardConfigWithStatus, 0, len(configs))
	for _, c := range configs {
		rules, err := forward_repo.Forward().ListRulesByConfigID(ctx, c.ID)
		if err != nil {
			logger.Default().Warn("list forward rules by config ID", zap.Error(err), zap.Int64("configID", c.ID))
		}

		// 获取资产名
		assetName := ""
		if asset, err := asset_svc.Asset().Get(ctx, c.AssetID); err == nil {
			assetName = asset.Name
		}

		rulesWithStatus := make([]RuleWithStatus, 0, len(rules))
		for _, r := range rules {
			rs := s.forwardManager.GetRuleStatus(r.ID)
			rulesWithStatus = append(rulesWithStatus, RuleWithStatus{
				ForwardRule: *r,
				Status:      rs.Status,
				Error:       rs.Error,
			})
		}

		result = append(result, ForwardConfigWithStatus{
			ForwardConfig: *c,
			AssetName:     assetName,
			Rules:         rulesWithStatus,
			Status:        s.forwardManager.GetConfigStatus(c.ID),
		})
	}
	return result, nil
}

// StartForwardConfig 启动转发配置
func (s *SSH) StartForwardConfig(id int64) error {
	return s.forwardManager.StartConfig(i18n.Ctx(s.ctx, s.lang.Lang()), id)
}

// StopForwardConfig 停止转发配置
func (s *SSH) StopForwardConfig(id int64) {
	s.forwardManager.StopConfig(id)
}
