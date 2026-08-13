package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/service/credential_query_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCredentialQueryService struct {
	listOptions credential_query_svc.ListOptions
	getRef      string
}

func (f *fakeCredentialQueryService) List(_ context.Context, opts credential_query_svc.ListOptions) ([]credential_query_svc.Summary, error) {
	f.listOptions = opts
	return []credential_query_svc.Summary{{Ref: "credential:1", ID: 1, Type: credential_query_svc.TypePassword, Name: "db", Username: "app"}}, nil
}

func (f *fakeCredentialQueryService) Get(_ context.Context, ref string) (*credential_query_svc.Detail, error) {
	f.getRef = ref
	return &credential_query_svc.Detail{Summary: credential_query_svc.Summary{Ref: ref, ID: 1, Type: credential_query_svc.TypePassword, Name: "db"}, Assets: []credential_query_svc.AssetUsage{}}, nil
}

func TestCredentialHandlersUseSharedQueryService(t *testing.T) {
	fake := &fakeCredentialQueryService{}
	original := credential_query_svc.Default()
	credential_query_svc.Register(fake)
	t.Cleanup(func() { credential_query_svc.Register(original) })

	listJSON, err := handleListCredentials(context.Background(), map[string]any{"type": "ssh_key"})
	require.NoError(t, err)
	assert.Equal(t, credential_query_svc.TypeSSHKey, fake.listOptions.Type)
	var listed []map[string]any
	require.NoError(t, json.Unmarshal([]byte(listJSON), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "credential:1", listed[0]["ref"])

	getJSON, err := handleGetCredential(context.Background(), map[string]any{"ref": "credential:1"})
	require.NoError(t, err)
	assert.Equal(t, "credential:1", fake.getRef)
	assert.Contains(t, getJSON, `"assets":[]`)
}

func TestGetCredentialHandlerRequiresTypedRef(t *testing.T) {
	_, err := handleGetCredential(context.Background(), map[string]any{})
	assert.EqualError(t, err, "missing required parameter: ref")
}
