package credential_mgr_svc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupInvalidation 登记一个记录失效资产 ID 的假失效器，并把引用查找指向 refs（凭证 ID -> 资产 ID）。
func setupInvalidation(t *testing.T, refs map[int64][]int64, lookupErr error) func() []int64 {
	t.Helper()
	var mu sync.Mutex
	var got []int64
	assetconn.RegisterInvalidator("cred_mgr_test", func(_ context.Context, id int64) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, id)
		return nil
	})
	SetReferencingAssetsLookup(func(_ context.Context, credID int64) ([]int64, error) {
		return refs[credID], lookupErr
	})
	t.Cleanup(func() {
		assetconn.UnregisterForTest("cred_mgr_test")
		SetReferencingAssetsLookup(nil)
	})
	return func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), got...)
	}
}

func TestUpdatePassword_InvalidatesReferencingAssets(t *testing.T) {
	setupCredentialTestEnv(t)
	ctx := context.Background()
	cred, err := CreatePassword(ctx, CreatePasswordRequest{Name: "p", Username: "u", Password: "old"})
	require.NoError(t, err)
	other, err := CreatePassword(ctx, CreatePasswordRequest{Name: "q", Username: "u", Password: "old"})
	require.NoError(t, err)
	got := setupInvalidation(t, map[int64][]int64{cred.ID: {11, 12}, other.ID: {99}}, nil)

	require.NoError(t, UpdatePassword(ctx, cred.ID, "new"))

	assert.ElementsMatch(t, []int64{11, 12}, got())
}

func TestUpdatePassphrase_InvalidatesReferencingAssets(t *testing.T) {
	setupCredentialTestEnv(t)
	ctx := context.Background()
	cred, err := GenerateSSHKey(ctx, GenerateKeyRequest{Name: "k", KeyType: credential_entity.KeyTypeED25519})
	require.NoError(t, err)
	got := setupInvalidation(t, map[int64][]int64{cred.ID: {21}, cred.ID + 1: {99}}, nil)

	require.NoError(t, UpdatePassphrase(ctx, cred.ID, "", "np"))

	assert.Equal(t, []int64{21}, got())
}

func TestUpdate_InvalidatesOnlyWhenUsernameChanges(t *testing.T) {
	setupCredentialTestEnv(t)
	ctx := context.Background()
	cred, err := CreatePassword(ctx, CreatePasswordRequest{Name: "p", Username: "u1", Password: "x"})
	require.NoError(t, err)
	got := setupInvalidation(t, map[int64][]int64{cred.ID: {31}}, nil)

	_, err = Update(ctx, UpdateRequest{ID: cred.ID, Name: "renamed", Description: "d", Username: "u1"})
	require.NoError(t, err)
	assert.Empty(t, got(), "name/description are not auth-relevant")

	_, err = Update(ctx, UpdateRequest{ID: cred.ID, Name: "renamed", Username: "u2"})
	require.NoError(t, err)
	assert.Equal(t, []int64{31}, got())
}

func TestFailedUpdateInvalidatesNothing(t *testing.T) {
	setupCredentialTestEnv(t)
	ctx := context.Background()
	cred, err := CreatePassword(ctx, CreatePasswordRequest{Name: "p", Username: "u", Password: "x"})
	require.NoError(t, err)
	got := setupInvalidation(t, map[int64][]int64{cred.ID: {41}, 777: {42}}, nil)

	assert.Error(t, UpdatePassword(ctx, 777, "new"))          // 凭证不存在
	assert.Error(t, UpdatePassphrase(ctx, cred.ID, "", "np")) // 类型不是 SSH 密钥
	_, err = Update(ctx, UpdateRequest{ID: 777, Name: "n", Username: "z"})
	assert.Error(t, err)

	assert.Empty(t, got())
}

func TestUpdatePassword_LookupFailureDoesNotUndoSave(t *testing.T) {
	repo := setupCredentialTestEnv(t)
	ctx := context.Background()
	cred, err := CreatePassword(ctx, CreatePasswordRequest{Name: "p", Username: "u", Password: "old"})
	require.NoError(t, err)
	before := cred.Password
	got := setupInvalidation(t, nil, errors.New("db down"))

	require.NoError(t, UpdatePassword(ctx, cred.ID, "new"))

	persisted, err := repo.Find(ctx, cred.ID)
	require.NoError(t, err)
	assert.NotEqual(t, before, persisted.Password)
	assert.Empty(t, got())
}
