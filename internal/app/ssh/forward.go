package ssh

import (
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/forward_entity"
)

func (s *SSH) CreateForwardConfig(name string, assetID int64, rules []forward_entity.ForwardRule) (*forward_entity.ForwardConfig, error) {
	return s.forward.Create(i18n.Ctx(s.ctx, s.lang.Lang()), name, assetID, rules)
}

func (s *SSH) UpdateForwardConfig(id int64, name string, assetID int64, rules []forward_entity.ForwardRule) (*forward_entity.ForwardConfig, error) {
	return s.forward.Update(i18n.Ctx(s.ctx, s.lang.Lang()), id, name, assetID, rules)
}

func (s *SSH) DeleteForwardConfig(id int64) error {
	return s.forward.Delete(i18n.Ctx(s.ctx, s.lang.Lang()), id)
}

func (s *SSH) ListForwardConfigs() ([]ForwardConfigWithStatus, error) {
	configs, err := s.forward.List(i18n.Ctx(s.ctx, s.lang.Lang()))
	if err != nil {
		return nil, err
	}
	result := make([]ForwardConfigWithStatus, 0, len(configs))
	for _, config := range configs {
		rules := make([]RuleWithStatus, 0, len(config.Rules))
		for _, rule := range config.Rules {
			rules = append(rules, RuleWithStatus{
				ForwardRule: rule.ForwardRule,
				Status:      rule.Status,
				Error:       rule.Error,
			})
		}
		result = append(result, ForwardConfigWithStatus{
			ForwardConfig: config.ForwardConfig,
			AssetName:     config.AssetName,
			Rules:         rules,
			Status:        config.Status,
		})
	}
	return result, nil
}

func (s *SSH) StartForwardConfig(id int64) error {
	return s.forward.Start(i18n.Ctx(s.ctx, s.lang.Lang()), id)
}

func (s *SSH) StopForwardConfig(id int64) {
	s.forward.Stop(id)
}
