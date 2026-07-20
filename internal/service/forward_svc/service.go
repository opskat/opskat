// Package forward_svc owns persisted SSH port-forward configuration workflows.
package forward_svc

import (
	"context"
	"time"

	"github.com/opskat/opskat/internal/model/entity/forward_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/forward_repo"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// Runtime is the live port-forward state managed by the SSH subsystem.
type Runtime interface {
	StartConfig(ctx context.Context, configID int64) error
	StopConfig(configID int64)
	IsConfigRunning(configID int64) bool
	GetConfigStatus(configID int64) string
	GetRuleStatus(ruleID int64) RuleStatus
}

type RuleStatus struct {
	RuleID int64  `json:"ruleId"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type ConfigWithStatus struct {
	forward_entity.ForwardConfig
	AssetName string           `json:"assetName"`
	Rules     []RuleWithStatus `json:"rules"`
	Status    string           `json:"status"`
}

type RuleWithStatus struct {
	forward_entity.ForwardRule
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Service struct {
	runtime Runtime
	now     func() int64
}

func New(runtime Runtime) *Service {
	return &Service{runtime: runtime, now: func() int64 { return time.Now().Unix() }}
}

func (s *Service) Create(ctx context.Context, name string, assetID int64, rules []forward_entity.ForwardRule) (*forward_entity.ForwardConfig, error) {
	now := s.now()
	config := &forward_entity.ForwardConfig{Name: name, AssetID: assetID, Createtime: now, Updatetime: now}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := forward_repo.Forward().CreateConfig(ctx, config); err != nil {
		return nil, err
	}
	if err := forward_repo.Forward().ReplaceRules(ctx, config.ID, rulePointers(rules)); err != nil {
		return nil, err
	}
	return config, nil
}

func (s *Service) Update(ctx context.Context, id int64, name string, assetID int64, rules []forward_entity.ForwardRule) (*forward_entity.ForwardConfig, error) {
	config, err := forward_repo.Forward().FindConfig(ctx, id)
	if err != nil {
		return nil, err
	}

	wasRunning := s.runtime.IsConfigRunning(id)
	if wasRunning {
		s.runtime.StopConfig(id)
	}
	config.Name = name
	config.AssetID = assetID
	config.Updatetime = s.now()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := forward_repo.Forward().UpdateConfig(ctx, config); err != nil {
		return nil, err
	}
	if err := forward_repo.Forward().ReplaceRules(ctx, config.ID, rulePointers(rules)); err != nil {
		return nil, err
	}

	if wasRunning {
		if err := s.runtime.StartConfig(ctx, id); err != nil {
			logger.Ctx(ctx).Error("restart forward config after update", zap.Int64("id", id), zap.Error(err))
		}
	}
	return config, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	s.runtime.StopConfig(id)
	if err := forward_repo.Forward().DeleteRulesByConfigID(ctx, id); err != nil {
		return err
	}
	return forward_repo.Forward().DeleteConfig(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]ConfigWithStatus, error) {
	configs, err := forward_repo.Forward().ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ConfigWithStatus, 0, len(configs))
	for _, config := range configs {
		rules, rulesErr := forward_repo.Forward().ListRulesByConfigID(ctx, config.ID)
		if rulesErr != nil {
			logger.Ctx(ctx).Warn("list forward rules by config ID", zap.Error(rulesErr), zap.Int64("configID", config.ID))
		}
		assetName := ""
		if asset, assetErr := asset_repo.Asset().Find(ctx, config.AssetID); assetErr == nil {
			assetName = asset.Name
		}
		rulesWithStatus := make([]RuleWithStatus, 0, len(rules))
		for _, rule := range rules {
			status := s.runtime.GetRuleStatus(rule.ID)
			rulesWithStatus = append(rulesWithStatus, RuleWithStatus{ForwardRule: *rule, Status: status.Status, Error: status.Error})
		}
		result = append(result, ConfigWithStatus{
			ForwardConfig: *config,
			AssetName:     assetName,
			Rules:         rulesWithStatus,
			Status:        s.runtime.GetConfigStatus(config.ID),
		})
	}
	return result, nil
}

func (s *Service) Start(ctx context.Context, id int64) error { return s.runtime.StartConfig(ctx, id) }

func (s *Service) Stop(id int64) { s.runtime.StopConfig(id) }

func rulePointers(rules []forward_entity.ForwardRule) []*forward_entity.ForwardRule {
	result := make([]*forward_entity.ForwardRule, len(rules))
	for i := range rules {
		result[i] = &rules[i]
	}
	return result
}
