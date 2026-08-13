package ssh_agent_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/sshagent"
	"go.uber.org/zap"
)

// SourceMetadata is a safe persisted source projection that never exposes the endpoint value.
type SourceMetadata struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	EndpointType string `json:"endpoint_type"`
	Description  string `json:"description,omitempty"`
	Createtime   int64  `json:"createtime"`
	Updatetime   int64  `json:"updatetime"`
}

// Observation combines the safe runtime status, identity summaries and source usage.
type Observation struct {
	Status        ProbeStatus       `json:"availability"`
	IdentityCount int               `json:"identity_count"`
	Usages        int64             `json:"usages"`
	Identities    []IdentitySummary `json:"identities"`
}

var observeIdentities = func(ctx context.Context, endpointType, endpoint string) ([]IdentitySummary, error) {
	ids, err := inspectIdentities(ctx, sshagent.Source{Type: sshagent.EndpointType(endpointType), Value: endpoint})
	if err != nil {
		return nil, err
	}
	result := make([]IdentitySummary, 0, len(ids))
	for _, ident := range ids {
		result = append(result, IdentitySummary{
			Fingerprint: ident.Fingerprint,
			Type:        ident.Type,
			Comment:     ident.Comment,
		})
	}
	return result, nil
}

func metadataOf(src *ssh_agent_source_entity.SSHAgentSource) SourceMetadata {
	return SourceMetadata{
		ID:           src.ID,
		Name:         src.Name,
		EndpointType: src.EndpointType,
		Description:  src.Description,
		Createtime:   src.Createtime,
		Updatetime:   src.Updatetime,
	}
}

// ListMetadata returns persisted source metadata without endpoint values.
func ListMetadata(ctx context.Context) ([]SourceMetadata, error) {
	sources, err := List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]SourceMetadata, len(sources))
	for i, source := range sources {
		result[i] = metadataOf(source)
	}
	return result, nil
}

// GetMetadata returns one persisted source without its endpoint value.
func GetMetadata(ctx context.Context, id int64) (SourceMetadata, error) {
	source, err := sourceOrErr(ctx, id)
	if err != nil {
		return SourceMetadata{}, err
	}
	return metadataOf(source), nil
}

// Observe reports a source's expected availability states without failing for runtime unavailability.
// Repository failures while loading the source or usage still fail the operation.
func Observe(ctx context.Context, id int64) (Observation, error) {
	logger.Ctx(ctx).Info("ssh agent source observation start", zap.Int64("sourceID", id))
	source, err := sourceOrErr(ctx, id)
	if err != nil {
		logger.Ctx(ctx).Error("ssh agent source observation failed", zap.Int64("sourceID", id), zap.Error(err))
		return Observation{}, err
	}
	usages, err := asset_repo.Asset().CountAgentAuthBySourceID(ctx, id)
	if err != nil {
		logger.Ctx(ctx).Error("ssh agent source observation failed", zap.Int64("sourceID", id), zap.Error(err))
		return Observation{}, err
	}

	identities, err := observeIdentities(ctx, source.EndpointType, source.Endpoint)
	if err != nil {
		if code, ok := sshagent.CodeOf(err); ok {
			switch code {
			case sshagent.CodePlatformUnsupported:
				return observedUnavailable(ctx, id, ProbeUnsupported, usages), nil
			case sshagent.CodeEmpty:
				return observedUnavailable(ctx, id, ProbeEmpty, usages), nil
			case sshagent.CodeEnvUnset, sshagent.CodeEndpointUnavailable,
				sshagent.CodeProtocolError, sshagent.CodePayloadInvalid, sshagent.CodeCancelled:
				return observedUnavailable(ctx, id, ProbeUnavailable, usages), nil
			}
		}
		logger.Ctx(ctx).Error("ssh agent source observation failed", zap.Int64("sourceID", id), zap.Error(err))
		return Observation{}, err
	}
	perIdentity, err := asset_repo.Asset().CountAgentAuthBySourceIDGroupByFingerprint(ctx, id)
	if err != nil {
		logger.Ctx(ctx).Error("ssh agent source observation failed", zap.Int64("sourceID", id), zap.Error(err))
		return Observation{}, err
	}
	for i := range identities {
		identities[i].Usages = perIdentity[identities[i].Fingerprint]
	}
	result := Observation{Status: ProbeOK, IdentityCount: len(identities), Usages: usages, Identities: identities}
	if result.IdentityCount == 0 {
		result.Status = ProbeEmpty
	}
	logger.Ctx(ctx).Info("ssh agent source observation end", zap.Int64("sourceID", id), zap.String("availability", string(result.Status)), zap.Int("identities", result.IdentityCount), zap.Int64("usages", result.Usages))
	return result, nil
}

func observedUnavailable(ctx context.Context, id int64, status ProbeStatus, usages int64) Observation {
	result := Observation{Status: status, Usages: usages, Identities: []IdentitySummary{}}
	logger.Ctx(ctx).Info("ssh agent source observation end", zap.Int64("sourceID", id), zap.String("availability", string(status)), zap.Int64("usages", usages))
	return result
}
