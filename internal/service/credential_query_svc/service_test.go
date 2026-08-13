package credential_query_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/service/asset_credential_svc"
	"github.com/opskat/opskat/internal/service/ssh_agent_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRefRequiresTypedPositiveID(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want Ref
	}{
		{name: "credential", in: "credential:12", want: Ref{Kind: RefCredential, ID: 12}},
		{name: "agent source", in: "agent-source:7", want: Ref{Kind: RefAgentSource, ID: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRef(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	for _, in := range []string{"12", "", "credential:", "credential:0", "credential:-1", "credential:+1", "credential:x", "agent-source:1:2", "password:1"} {
		t.Run("reject "+in, func(t *testing.T) {
			_, err := ParseRef(in)
			assert.Error(t, err)
		})
	}
}

func TestListReturnsStableSafeSummariesAndDegradesUnavailableAgent(t *testing.T) {
	deps := dependencies{
		listCredentials: func(context.Context) ([]*credential_entity.Credential, error) {
			return []*credential_entity.Credential{
				{ID: 2, Name: "deploy", Type: credential_entity.TypeSSHKey, Username: "root", PrivateKey: "cipher-private", Passphrase: "cipher-passphrase", PublicKey: "ssh-ed25519 AAAA public", KeyType: "ed25519", KeySize: 256, Fingerprint: "SHA256:key", Comment: "deploy key", Createtime: 20, Updatetime: 21},
				{ID: 1, Name: "database", Type: credential_entity.TypePassword, Username: "app", Password: "cipher-password", Description: "prod", Createtime: 10, Updatetime: 11},
			}, nil
		},
		listAgentSources: func(context.Context) ([]ssh_agent_svc.SourceMetadata, error) {
			return []ssh_agent_svc.SourceMetadata{
				{ID: 4, Name: "offline", EndpointType: "unix_socket", Description: "laptop", Createtime: 30, Updatetime: 31},
				{ID: 3, Name: "work", EndpointType: "environment", Createtime: 40, Updatetime: 41},
			}, nil
		},
		observeAgent: func(_ context.Context, id int64) (ssh_agent_svc.Observation, error) {
			if id == 4 {
				return ssh_agent_svc.Observation{Status: ssh_agent_svc.ProbeUnavailable, Usages: 2}, nil
			}
			return ssh_agent_svc.Observation{Status: ssh_agent_svc.ProbeOK, IdentityCount: 1, Usages: 1}, nil
		},
	}

	got, err := newService(deps).List(context.Background(), ListOptions{})
	require.NoError(t, err)
	require.Len(t, got, 4)
	assert.Equal(t, []string{"credential:1", "credential:2", "agent-source:3", "agent-source:4"}, []string{got[0].Ref, got[1].Ref, got[2].Ref, got[3].Ref})
	assert.Equal(t, "unavailable", string(got[3].Availability))
	assert.Equal(t, int64(2), got[3].Usages)

	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	text := string(encoded)
	for _, forbidden := range []string{"cipher-password", "cipher-private", "cipher-passphrase", "ssh-ed25519 AAAA public", "password_present", "endpoint_value"} {
		assert.NotContains(t, text, forbidden)
	}
	assert.Contains(t, text, `"endpoint_type":"unix_socket"`)
}

func TestListFiltersKindsAndRejectsUnknownFilter(t *testing.T) {
	credentialCalls := 0
	agentCalls := 0
	deps := dependencies{
		listCredentials: func(context.Context) ([]*credential_entity.Credential, error) {
			credentialCalls++
			return []*credential_entity.Credential{
				{ID: 1, Name: "password", Type: credential_entity.TypePassword},
				{ID: 2, Name: "key", Type: credential_entity.TypeSSHKey},
			}, nil
		},
		listAgentSources: func(context.Context) ([]ssh_agent_svc.SourceMetadata, error) {
			agentCalls++
			return []ssh_agent_svc.SourceMetadata{{ID: 3, Name: "agent", EndpointType: "unix_socket"}}, nil
		},
		observeAgent: func(context.Context, int64) (ssh_agent_svc.Observation, error) {
			return ssh_agent_svc.Observation{Status: ssh_agent_svc.ProbeEmpty}, nil
		},
	}
	svc := newService(deps)

	passwords, err := svc.List(context.Background(), ListOptions{Type: TypePassword})
	require.NoError(t, err)
	require.Len(t, passwords, 1)
	assert.Equal(t, TypePassword, passwords[0].Type)
	assert.Equal(t, 1, credentialCalls)
	assert.Equal(t, 0, agentCalls)

	agents, err := svc.List(context.Background(), ListOptions{Type: TypeSSHAgent})
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, TypeSSHAgent, agents[0].Type)
	assert.Equal(t, 1, agentCalls)

	_, err = svc.List(context.Background(), ListOptions{Type: "token"})
	assert.EqualError(t, err, "unsupported credential type filter")
}

func TestGetCredentialReturnsUsageAndOnlyKeyPublicMaterial(t *testing.T) {
	deps := dependencies{
		getCredential: func(_ context.Context, id int64) (*credential_entity.Credential, error) {
			return &credential_entity.Credential{ID: id, Name: "deploy", Type: credential_entity.TypeSSHKey, Username: "root", PrivateKey: "cipher-private", Passphrase: "cipher-passphrase", PublicKey: "ssh-ed25519 AAAA public", Fingerprint: "SHA256:key"}, nil
		},
		usageAssets: func(_ context.Context, id int64) ([]asset_credential_svc.AssetUsage, error) {
			return []asset_credential_svc.AssetUsage{{ID: 9, Name: "web", Type: "ssh"}}, nil
		},
	}

	detail, err := newService(deps).Get(context.Background(), "credential:2")
	require.NoError(t, err)
	assert.Equal(t, "credential:2", detail.Ref)
	assert.Equal(t, "ssh-ed25519 AAAA public", detail.PublicKey)
	assert.Equal(t, []asset_credential_svc.AssetUsage{{ID: 9, Name: "web", Type: "ssh"}}, detail.Assets)

	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "cipher-private")
	assert.NotContains(t, string(encoded), "cipher-passphrase")
}

func TestGetAgentReturnsSafeMetadataIdentitiesAndExpectedOfflineState(t *testing.T) {
	deps := dependencies{
		getAgentSource: func(_ context.Context, id int64) (ssh_agent_svc.SourceMetadata, error) {
			return ssh_agent_svc.SourceMetadata{ID: id, Name: "work", EndpointType: "unix_socket", Description: "desktop"}, nil
		},
		observeAgent: func(context.Context, int64) (ssh_agent_svc.Observation, error) {
			return ssh_agent_svc.Observation{
				Status: ssh_agent_svc.ProbeOK, IdentityCount: 1, Usages: 3,
				Identities: []ssh_agent_svc.IdentitySummary{{Fingerprint: "SHA256:key", Type: "ssh-ed25519", Comment: "safe", Usages: 2}},
			}, nil
		},
	}

	detail, err := newService(deps).Get(context.Background(), "agent-source:7")
	require.NoError(t, err)
	assert.Equal(t, TypeSSHAgent, detail.Type)
	assert.Equal(t, "unix_socket", detail.EndpointType)
	assert.Equal(t, ssh_agent_svc.ProbeOK, detail.Availability)
	assert.Equal(t, int64(3), detail.Usages)
	require.Len(t, detail.Identities, 1)

	deps.observeAgent = func(context.Context, int64) (ssh_agent_svc.Observation, error) {
		return ssh_agent_svc.Observation{Status: ssh_agent_svc.ProbeUnsupported, Usages: 3, Identities: []ssh_agent_svc.IdentitySummary{}}, nil
	}
	offline, err := newService(deps).Get(context.Background(), "agent-source:7")
	require.NoError(t, err)
	assert.Equal(t, ssh_agent_svc.ProbeUnsupported, offline.Availability)
	assert.Empty(t, offline.Identities)
	encoded, err := json.Marshal(offline)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"identities":[]`)
	assert.NotContains(t, string(encoded), "endpoint_value")
}

func TestRepositoryErrorsFailQueries(t *testing.T) {
	repoErr := errors.New("database unavailable")

	_, err := newService(dependencies{
		listCredentials: func(context.Context) ([]*credential_entity.Credential, error) { return nil, repoErr },
	}).List(context.Background(), ListOptions{Type: TypePassword})
	assert.ErrorIs(t, err, repoErr)

	_, err = newService(dependencies{
		listAgentSources: func(context.Context) ([]ssh_agent_svc.SourceMetadata, error) { return nil, repoErr },
	}).List(context.Background(), ListOptions{Type: TypeSSHAgent})
	assert.ErrorIs(t, err, repoErr)

	_, err = newService(dependencies{
		getCredential: func(context.Context, int64) (*credential_entity.Credential, error) {
			return &credential_entity.Credential{ID: 1, Type: credential_entity.TypePassword}, nil
		},
		usageAssets: func(context.Context, int64) ([]asset_credential_svc.AssetUsage, error) { return nil, repoErr },
	}).Get(context.Background(), "credential:1")
	assert.ErrorIs(t, err, repoErr)
}
