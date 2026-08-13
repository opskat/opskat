// Package credential_query_svc provides the shared safe credential read boundary for AI and opsctl.
package credential_query_svc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/service/asset_credential_svc"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
	"go.uber.org/zap"
)

const (
	TypePassword = credential_entity.TypePassword
	TypeSSHKey   = credential_entity.TypeSSHKey
	TypeSSHAgent = "ssh_agent"
)

type RefKind string

const (
	RefCredential  RefKind = "credential"
	RefAgentSource RefKind = "agent-source"
)

type Ref struct {
	Kind RefKind
	ID   int64
}

func (r Ref) String() string {
	return string(r.Kind) + ":" + strconv.FormatInt(r.ID, 10)
}

// ParseRef rejects ambiguous bare IDs and accepts only positive typed references.
func ParseRef(value string) (Ref, error) {
	kindText, idText, ok := strings.Cut(value, ":")
	if !ok || kindText == "" || idText == "" || strings.Contains(idText, ":") {
		return Ref{}, fmt.Errorf("invalid credential ref: expected credential:<id> or agent-source:<id>")
	}
	if strings.Trim(idText, "0123456789") != "" {
		return Ref{}, fmt.Errorf("invalid credential ref: expected a positive numeric id")
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return Ref{}, fmt.Errorf("invalid credential ref: expected a positive numeric id")
	}
	kind := RefKind(kindText)
	if kind != RefCredential && kind != RefAgentSource {
		return Ref{}, fmt.Errorf("invalid credential ref type")
	}
	return Ref{Kind: kind, ID: id}, nil
}

type ListOptions struct {
	Type string
}

type AssetUsage = asset_credential_svc.AssetUsage

type Summary struct {
	Ref           string                    `json:"ref"`
	ID            int64                     `json:"id"`
	Type          string                    `json:"type"`
	Name          string                    `json:"name"`
	Description   string                    `json:"description,omitempty"`
	Createtime    int64                     `json:"createtime"`
	Updatetime    int64                     `json:"updatetime"`
	Username      string                    `json:"username,omitempty"`
	KeyType       string                    `json:"key_type,omitempty"`
	KeySize       int                       `json:"key_size,omitempty"`
	Fingerprint   string                    `json:"fingerprint,omitempty"`
	Comment       string                    `json:"comment,omitempty"`
	EndpointType  string                    `json:"endpoint_type,omitempty"`
	Availability  ssh_agent_svc.ProbeStatus `json:"availability,omitempty"`
	IdentityCount int                       `json:"identity_count,omitempty"`
	Usages        int64                     `json:"usages,omitempty"`
}

type Detail struct {
	Summary
	Assets     []AssetUsage                    `json:"assets"`
	PublicKey  string                          `json:"public_key,omitempty"`
	Identities []ssh_agent_svc.IdentitySummary `json:"identities"`
}

type Service interface {
	List(ctx context.Context, opts ListOptions) ([]Summary, error)
	Get(ctx context.Context, ref string) (*Detail, error)
}

type dependencies struct {
	listCredentials  func(context.Context) ([]*credential_entity.Credential, error)
	getCredential    func(context.Context, int64) (*credential_entity.Credential, error)
	usageAssets      func(context.Context, int64) ([]asset_credential_svc.AssetUsage, error)
	listAgentSources func(context.Context) ([]ssh_agent_svc.SourceMetadata, error)
	getAgentSource   func(context.Context, int64) (ssh_agent_svc.SourceMetadata, error)
	observeAgent     func(context.Context, int64) (ssh_agent_svc.Observation, error)
}

type service struct {
	deps dependencies
}

func productionDependencies() dependencies {
	return dependencies{
		listCredentials:  credential_mgr_svc.List,
		getCredential:    credential_mgr_svc.Get,
		usageAssets:      asset_credential_svc.Default().UsageAssets,
		listAgentSources: ssh_agent_svc.ListMetadata,
		getAgentSource:   ssh_agent_svc.GetMetadata,
		observeAgent:     ssh_agent_svc.Observe,
	}
}

func newService(deps dependencies) Service {
	return &service{deps: deps}
}

var defaultService Service = newService(productionDependencies())

func Default() Service {
	return defaultService
}

// Register replaces the shared query service, primarily for boundary tests.
func Register(svc Service) {
	defaultService = svc
}

func validateFilter(value string) error {
	switch value {
	case "", TypePassword, TypeSSHKey, TypeSSHAgent:
		return nil
	default:
		return errors.New("unsupported credential type filter")
	}
}

func credentialSummary(cred *credential_entity.Credential) Summary {
	return Summary{
		Ref: credRef(cred.ID), ID: cred.ID, Type: cred.Type, Name: cred.Name,
		Description: cred.Description, Createtime: cred.Createtime, Updatetime: cred.Updatetime,
		Username: cred.Username, KeyType: cred.KeyType, KeySize: cred.KeySize,
		Fingerprint: cred.Fingerprint, Comment: cred.Comment,
	}
}

func agentSummary(source ssh_agent_svc.SourceMetadata, observation ssh_agent_svc.Observation) Summary {
	return Summary{
		Ref: agentRef(source.ID), ID: source.ID, Type: TypeSSHAgent, Name: source.Name,
		Description: source.Description, Createtime: source.Createtime, Updatetime: source.Updatetime,
		EndpointType: source.EndpointType, Availability: observation.Status,
		IdentityCount: observation.IdentityCount, Usages: observation.Usages,
	}
}

func credRef(id int64) string  { return Ref{Kind: RefCredential, ID: id}.String() }
func agentRef(id int64) string { return Ref{Kind: RefAgentSource, ID: id}.String() }

func (s *service) List(ctx context.Context, opts ListOptions) ([]Summary, error) {
	logger.Ctx(ctx).Info("credential query list start", zap.String("type", opts.Type))
	if err := validateFilter(opts.Type); err != nil {
		logger.Ctx(ctx).Error("credential query list failed", zap.String("type", opts.Type), zap.Error(err))
		return nil, err
	}
	result := make([]Summary, 0)

	if opts.Type == "" || opts.Type == TypePassword || opts.Type == TypeSSHKey {
		credentials, err := s.deps.listCredentials(ctx)
		if err != nil {
			logger.Ctx(ctx).Error("credential query list failed", zap.String("type", opts.Type), zap.Error(err))
			return nil, err
		}
		for _, credential := range credentials {
			if opts.Type != "" && credential.Type != opts.Type {
				continue
			}
			if credential.Type != TypePassword && credential.Type != TypeSSHKey {
				continue
			}
			result = append(result, credentialSummary(credential))
		}
	}

	if opts.Type == "" || opts.Type == TypeSSHAgent {
		sources, err := s.deps.listAgentSources(ctx)
		if err != nil {
			logger.Ctx(ctx).Error("credential query list failed", zap.String("type", opts.Type), zap.Error(err))
			return nil, err
		}
		for _, source := range sources {
			observation, err := s.deps.observeAgent(ctx, source.ID)
			if err != nil {
				logger.Ctx(ctx).Error("credential query list failed", zap.String("type", opts.Type), zap.Int64("sourceID", source.ID), zap.Error(err))
				return nil, err
			}
			result = append(result, agentSummary(source, observation))
		}
	}

	rank := func(item Summary) int {
		switch item.Type {
		case TypePassword:
			return 0
		case TypeSSHKey:
			return 1
		default:
			return 2
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if rank(result[i]) == rank(result[j]) {
			return result[i].ID < result[j].ID
		}
		return rank(result[i]) < rank(result[j])
	})
	logger.Ctx(ctx).Info("credential query list end", zap.String("type", opts.Type), zap.Int("count", len(result)))
	return result, nil
}

func (s *service) Get(ctx context.Context, value string) (*Detail, error) {
	logger.Ctx(ctx).Info("credential query get start")
	ref, err := ParseRef(value)
	if err != nil {
		logger.Ctx(ctx).Error("credential query get failed", zap.Error(err))
		return nil, err
	}

	var detail *Detail
	switch ref.Kind {
	case RefCredential:
		credential, err := s.deps.getCredential(ctx, ref.ID)
		if err != nil {
			logger.Ctx(ctx).Error("credential query get failed", zap.String("ref", ref.String()), zap.Error(err))
			return nil, err
		}
		if credential.Type != TypePassword && credential.Type != TypeSSHKey {
			return nil, fmt.Errorf("unsupported credential type %q", credential.Type)
		}
		assets, err := s.deps.usageAssets(ctx, ref.ID)
		if err != nil {
			logger.Ctx(ctx).Error("credential query get failed", zap.String("ref", ref.String()), zap.Error(err))
			return nil, err
		}
		detail = &Detail{Summary: credentialSummary(credential), Assets: assets}
		if credential.Type == TypeSSHKey {
			detail.PublicKey = credential.PublicKey
		}
	case RefAgentSource:
		source, err := s.deps.getAgentSource(ctx, ref.ID)
		if err != nil {
			logger.Ctx(ctx).Error("credential query get failed", zap.String("ref", ref.String()), zap.Error(err))
			return nil, err
		}
		observation, err := s.deps.observeAgent(ctx, ref.ID)
		if err != nil {
			logger.Ctx(ctx).Error("credential query get failed", zap.String("ref", ref.String()), zap.Error(err))
			return nil, err
		}
		detail = &Detail{Summary: agentSummary(source, observation), Identities: observation.Identities}
	}
	logger.Ctx(ctx).Info("credential query get end", zap.String("ref", ref.String()))
	return detail, nil
}
