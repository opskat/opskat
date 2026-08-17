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
	"gorm.io/gorm"
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
				//nolint:gosec // Intentional sentinel ciphertext verifies that credential secrets never reach DTO JSON.
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
			//nolint:gosec // Intentional sentinel ciphertext verifies that credential secrets never reach DTO JSON.
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

func TestGetAssetAuthenticationManagedCredentialStoredMissingAndRepositoryError(t *testing.T) {
	ctx := context.Background()
	svc := newAssetAuthenticationService(dependencies{
		getCredential: func(_ context.Context, id int64) (*credential_entity.Credential, error) {
			switch id {
			case 7:
				return &credential_entity.Credential{
					ID: 7, Type: credential_entity.TypeSSHKey, Name: "deploy", Username: "root",
				}, nil
			case 8:
				return nil, gorm.ErrRecordNotFound
			default:
				return nil, errors.New("database unavailable")
			}
		},
	})

	stored, err := svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
		Type: TypeSSHKey, Ref: "credential:7",
	})
	require.NoError(t, err)
	assert.Equal(t, AssetAuthentication{
		Type: TypeSSHKey, Ref: "credential:7", Name: "deploy", Username: "root", Availability: AvailabilityStored,
	}, *stored)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "public_key")

	missing, err := svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
		Type: TypePassword, Ref: "credential:8",
	})
	require.NoError(t, err)
	assert.Equal(t, AssetAuthentication{
		Type: TypePassword, Ref: "credential:8", Availability: AvailabilityMissing,
	}, *missing)

	_, err = svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
		Type: TypePassword, Ref: "credential:9",
	})
	assert.EqualError(t, err, "database unavailable")
}

func TestGetAssetAuthenticationAgentProjectsSelectedIdentityAndSafeRuntimeStates(t *testing.T) {
	ctx := context.Background()
	status := ssh_agent_svc.ProbeOK
	identities := []ssh_agent_svc.IdentitySummary{{
		Fingerprint: "SHA256:selected", Type: "ssh-ed25519", Comment: "safe comment",
	}}
	svc := newAssetAuthenticationService(dependencies{
		getAgentSource: func(_ context.Context, id int64) (ssh_agent_svc.SourceMetadata, error) {
			if id == 99 {
				return ssh_agent_svc.SourceMetadata{}, &ssh_agent_svc.Error{Code: ssh_agent_svc.CodeSourceNotFound, Message: "missing"}
			}
			return ssh_agent_svc.SourceMetadata{ID: id, Name: "work", EndpointType: "unix_socket"}, nil
		},
		observeAgent: func(context.Context, int64) (ssh_agent_svc.Observation, error) {
			return ssh_agent_svc.Observation{Status: status, IdentityCount: len(identities), Identities: identities}, nil
		},
	})

	available, err := svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
		Type: TypeSSHAgent, Ref: "agent-source:4", Fingerprint: "SHA256:selected",
	})
	require.NoError(t, err)
	assert.Equal(t, AssetAuthentication{
		Type: TypeSSHAgent, Ref: "agent-source:4", Name: "work", EndpointType: "unix_socket",
		Fingerprint: "SHA256:selected", Availability: AvailabilityOK, KeyType: "ssh-ed25519", Comment: "safe comment",
	}, *available)

	for _, tc := range []struct {
		name   string
		status ssh_agent_svc.ProbeStatus
	}{
		{name: "empty", status: ssh_agent_svc.ProbeEmpty},
		{name: "unavailable", status: ssh_agent_svc.ProbeUnavailable},
		{name: "unsupported", status: ssh_agent_svc.ProbeUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status = tc.status
			identities = nil
			got, err := svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
				Type: TypeSSHAgent, Ref: "agent-source:4", Fingerprint: "SHA256:selected",
			})
			require.NoError(t, err)
			assert.Equal(t, string(tc.status), got.Availability)
			assert.Empty(t, got.KeyType)
			assert.Empty(t, got.Comment)
		})
	}

	status = ssh_agent_svc.ProbeOK
	identities = []ssh_agent_svc.IdentitySummary{{Fingerprint: "SHA256:other", Type: "ssh-rsa", Comment: "other"}}
	missingIdentity, err := svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
		Type: TypeSSHAgent, Ref: "agent-source:4", Fingerprint: "SHA256:selected",
	})
	require.NoError(t, err)
	assert.Equal(t, AvailabilityMissing, missingIdentity.Availability)
	assert.Empty(t, missingIdentity.KeyType)
	assert.Empty(t, missingIdentity.Comment)

	missingSource, err := svc.GetAssetAuthentication(ctx, AssetAuthenticationRequest{
		Type: TypeSSHAgent, Ref: "agent-source:99", Fingerprint: "SHA256:selected",
	})
	require.NoError(t, err)
	assert.Equal(t, AssetAuthentication{
		Type: TypeSSHAgent, Ref: "agent-source:99", Fingerprint: "SHA256:selected", Availability: AvailabilityMissing,
	}, *missingSource)

	encoded, err := json.Marshal(available)
	require.NoError(t, err)
	for _, forbidden := range []string{"endpoint_value", "public_key", "signature", "challenge", "private_key", "password"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
}

func TestGetAssetAuthenticationAgentPropagatesRepositoryErrors(t *testing.T) {
	repoErr := errors.New("database unavailable")
	svc := newAssetAuthenticationService(dependencies{
		getAgentSource: func(context.Context, int64) (ssh_agent_svc.SourceMetadata, error) {
			return ssh_agent_svc.SourceMetadata{ID: 4, Name: "work", EndpointType: "environment"}, nil
		},
		observeAgent: func(context.Context, int64) (ssh_agent_svc.Observation, error) {
			return ssh_agent_svc.Observation{}, repoErr
		},
	})

	_, err := svc.GetAssetAuthentication(context.Background(), AssetAuthenticationRequest{
		Type: TypeSSHAgent, Ref: "agent-source:4", Fingerprint: "SHA256:selected",
	})
	assert.ErrorIs(t, err, repoErr)
}
