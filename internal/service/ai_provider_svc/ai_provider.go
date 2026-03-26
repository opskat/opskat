package ai_provider_svc

import (
	"context"
	"time"

	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type AIProviderSvc interface {
	List(ctx context.Context) ([]*ai_provider_entity.AIProvider, error)
	Get(ctx context.Context, id int64) (*ai_provider_entity.AIProvider, error)
	GetActive(ctx context.Context) (*ai_provider_entity.AIProvider, error)
	Create(ctx context.Context, p *ai_provider_entity.AIProvider, rawAPIKey string) error
	Update(ctx context.Context, p *ai_provider_entity.AIProvider, rawAPIKey string) error
	Delete(ctx context.Context, id int64) error
	SetActive(ctx context.Context, id int64) error
	DecryptAPIKey(p *ai_provider_entity.AIProvider) (string, error)
}

type aiProviderSvc struct{}

var defaultAIProvider = &aiProviderSvc{}

func AIProvider() AIProviderSvc {
	return defaultAIProvider
}

func (s *aiProviderSvc) List(ctx context.Context) ([]*ai_provider_entity.AIProvider, error) {
	return ai_provider_repo.AIProvider().List(ctx)
}

func (s *aiProviderSvc) Get(ctx context.Context, id int64) (*ai_provider_entity.AIProvider, error) {
	return ai_provider_repo.AIProvider().Find(ctx, id)
}

func (s *aiProviderSvc) GetActive(ctx context.Context) (*ai_provider_entity.AIProvider, error) {
	return ai_provider_repo.AIProvider().GetActive(ctx)
}

func (s *aiProviderSvc) Create(ctx context.Context, p *ai_provider_entity.AIProvider, rawAPIKey string) error {
	if rawAPIKey != "" {
		encrypted, err := credential_svc.Default().Encrypt(rawAPIKey)
		if err != nil {
			return err
		}
		p.APIKey = encrypted
	}
	now := time.Now().Unix()
	p.Createtime = now
	p.Updatetime = now
	return ai_provider_repo.AIProvider().Create(ctx, p)
}

func (s *aiProviderSvc) Update(ctx context.Context, p *ai_provider_entity.AIProvider, rawAPIKey string) error {
	if rawAPIKey != "" {
		encrypted, err := credential_svc.Default().Encrypt(rawAPIKey)
		if err != nil {
			return err
		}
		p.APIKey = encrypted
	}
	p.Updatetime = time.Now().Unix()
	return ai_provider_repo.AIProvider().Update(ctx, p)
}

func (s *aiProviderSvc) Delete(ctx context.Context, id int64) error {
	return ai_provider_repo.AIProvider().Delete(ctx, id)
}

func (s *aiProviderSvc) SetActive(ctx context.Context, id int64) error {
	if err := ai_provider_repo.AIProvider().ClearActive(ctx); err != nil {
		return err
	}
	p, err := ai_provider_repo.AIProvider().Find(ctx, id)
	if err != nil {
		return err
	}
	p.IsActive = true
	p.Updatetime = time.Now().Unix()
	return ai_provider_repo.AIProvider().Update(ctx, p)
}

func (s *aiProviderSvc) DecryptAPIKey(p *ai_provider_entity.AIProvider) (string, error) {
	if p.APIKey == "" {
		return "", nil
	}
	return credential_svc.Default().Decrypt(p.APIKey)
}
