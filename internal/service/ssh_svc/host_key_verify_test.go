package ssh_svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

type sshHostKeyRepoFake struct {
	stored      *host_key_entity.HostKey
	findErr     error
	upsertErr   error
	findKeyType string
}

func (r *sshHostKeyRepoFake) FindByHostPortKeyType(_ context.Context, _ string, _ int, keyType string) (*host_key_entity.HostKey, error) {
	r.findKeyType = keyType
	if r.findErr != nil {
		return nil, r.findErr
	}
	if r.stored == nil || r.stored.KeyType != keyType {
		return nil, gorm.ErrRecordNotFound
	}
	keyCopy := *r.stored
	return &keyCopy, nil
}
func (r *sshHostKeyRepoFake) Upsert(_ context.Context, key *host_key_entity.HostKey) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	keyCopy := *key
	r.stored = &keyCopy
	return nil
}
func (r *sshHostKeyRepoFake) Delete(context.Context, int64) error { return nil }
func (r *sshHostKeyRepoFake) List(context.Context) ([]*host_key_entity.HostKey, error) {
	return nil, nil
}

func newSSHHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	key, err := ssh.NewPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	return key
}

func withSSHHostKeyRepo(t *testing.T, repo host_key_repo.HostKeyRepo) {
	t.Helper()
	old := host_key_repo.HostKey()
	host_key_repo.RegisterHostKey(repo)
	t.Cleanup(func() { host_key_repo.RegisterHostKey(old) })
}

func TestMakeHostKeyCallbackSurfacesRepositoryFailures(t *testing.T) {
	key := newSSHHostKey(t)

	t.Run("read failure is not first use", func(t *testing.T) {
		repo := &sshHostKeyRepoFake{findErr: errors.New("database offline")}
		withSSHHostKeyRepo(t, repo)
		called := false
		err := MakeHostKeyCallback("host", 22, func(HostKeyEvent) HostKeyAction {
			called = true
			return HostKeyAcceptAndSave
		})("host", nil, key)
		require.ErrorContains(t, err, "database offline")
		require.False(t, called)
	})

	t.Run("save failure rejects the connection", func(t *testing.T) {
		repo := &sshHostKeyRepoFake{upsertErr: errors.New("disk full")}
		withSSHHostKeyRepo(t, repo)
		err := MakeHostKeyCallback("host", 22, func(HostKeyEvent) HostKeyAction {
			return HostKeyAcceptAndSave
		})("host", nil, key)
		require.ErrorContains(t, err, "disk full")
	})
}

func TestMakeHostKeyCallbackLooksUpThePresentedSSHKeyType(t *testing.T) {
	key := newSSHHostKey(t)
	repo := &sshHostKeyRepoFake{stored: &host_key_entity.HostKey{
		Host: "host", Port: 22, KeyType: host_key_entity.KeyTypeVNCRSA,
		PublicKey: "unrelated-vnc-key", Fingerprint: "SHA256:vnc",
	}}
	withSSHHostKeyRepo(t, repo)

	var event HostKeyEvent
	err := MakeHostKeyCallback("host", 22, func(got HostKeyEvent) HostKeyAction {
		event = got
		return HostKeyAcceptOnce
	})("host", nil, key)

	require.NoError(t, err)
	require.False(t, event.IsChanged)
	require.Equal(t, key.Type(), event.KeyType)
	require.Equal(t, key.Type(), repo.findKeyType)
}
