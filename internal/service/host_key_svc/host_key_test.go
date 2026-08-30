package host_key_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type memoryHostKeyRepo struct {
	keys      map[string]*host_key_entity.HostKey
	findErr   error
	upsertErr error
	upserts   int
}

func newMemoryHostKeyRepo(keys ...*host_key_entity.HostKey) *memoryHostKeyRepo {
	r := &memoryHostKeyRepo{keys: make(map[string]*host_key_entity.HostKey)}
	for _, key := range keys {
		keyCopy := *key
		r.keys[hostKeyMapKey(keyCopy.Host, keyCopy.Port, keyCopy.KeyType)] = &keyCopy
	}
	return r
}

func hostKeyMapKey(host string, port int, keyType string) string {
	return host + "\x00" + keyType + "\x00" + string(rune(port))
}

func (r *memoryHostKeyRepo) FindByHostPortKeyType(_ context.Context, host string, port int, keyType string) (*host_key_entity.HostKey, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	key := r.keys[hostKeyMapKey(host, port, keyType)]
	if key == nil {
		return nil, gorm.ErrRecordNotFound
	}
	keyCopy := *key
	return &keyCopy, nil
}

func (r *memoryHostKeyRepo) Upsert(_ context.Context, key *host_key_entity.HostKey) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.upserts++
	keyCopy := *key
	r.keys[hostKeyMapKey(key.Host, key.Port, key.KeyType)] = &keyCopy
	return nil
}

func (r *memoryHostKeyRepo) Delete(context.Context, int64) error { return nil }
func (r *memoryHostKeyRepo) List(context.Context) ([]*host_key_entity.HostKey, error) {
	return nil, nil
}

func TestHostKeyServiceFirstUseMatchChangeAndCancellation(t *testing.T) {
	ctx := context.Background()
	old := &host_key_entity.HostKey{
		ID: 7, Host: "vnc.example", Port: 5901, KeyType: host_key_entity.KeyTypeVNCRSA,
		PublicKey: "old-key", Fingerprint: "SHA256:old", FirstSeen: 10, LastSeen: 20,
	}
	repo := newMemoryHostKeyRepo(old)
	svc := New(repo)

	first, err := svc.Check(ctx, PresentedKey{
		Host: "new.example", Port: 5902, KeyType: host_key_entity.KeyTypeVNCRSA,
		PublicKey: "first-key", Fingerprint: "SHA256:first",
	})
	require.NoError(t, err)
	require.Equal(t, CheckFirstUse, first.State)
	require.Equal(t, "SHA256:first", first.NewFingerprint)
	require.Empty(t, first.OldFingerprint)
	require.Zero(t, repo.upserts)

	require.NoError(t, svc.Trust(ctx, first.Key, false))
	persisted := repo.keys[hostKeyMapKey("new.example", 5902, host_key_entity.KeyTypeVNCRSA)]
	require.Equal(t, "first-key", persisted.PublicKey)
	require.Equal(t, persisted.FirstSeen, persisted.LastSeen)

	beforeMatch := repo.upserts
	match, err := svc.Check(ctx, PresentedKey{
		Host: "vnc.example", Port: 5901, KeyType: host_key_entity.KeyTypeVNCRSA,
		PublicKey: "old-key", Fingerprint: "SHA256:old",
	})
	require.NoError(t, err)
	require.Equal(t, CheckMatch, match.State)
	require.Greater(t, repo.upserts, beforeMatch, "an exact match must persist last-seen")
	require.GreaterOrEqual(t, repo.keys[hostKeyMapKey("vnc.example", 5901, host_key_entity.KeyTypeVNCRSA)].LastSeen, int64(20))

	changed, err := svc.Check(ctx, PresentedKey{
		Host: "vnc.example", Port: 5901, KeyType: host_key_entity.KeyTypeVNCRSA,
		PublicKey: "new-key", Fingerprint: "SHA256:new",
	})
	require.NoError(t, err)
	require.Equal(t, CheckChanged, changed.State)
	require.Equal(t, "SHA256:old", changed.OldFingerprint)
	require.Equal(t, "SHA256:new", changed.NewFingerprint)

	err = svc.Trust(ctx, changed.Key, false)
	require.ErrorIs(t, err, ErrChangedKeyRequiresReplacement)
	require.Equal(t, "old-key", repo.keys[hostKeyMapKey("vnc.example", 5901, host_key_entity.KeyTypeVNCRSA)].PublicKey)

	require.NoError(t, svc.Trust(ctx, changed.Key, true))
	replaced := repo.keys[hostKeyMapKey("vnc.example", 5901, host_key_entity.KeyTypeVNCRSA)]
	require.Equal(t, "new-key", replaced.PublicKey)
	require.Equal(t, "SHA256:new", replaced.Fingerprint)
	require.Equal(t, int64(10), replaced.FirstSeen)
}

func TestHostKeyServiceSurfacesReadAndPersistenceFailures(t *testing.T) {
	ctx := context.Background()
	presented := PresentedKey{Host: "host", Port: 5900, KeyType: host_key_entity.KeyTypeVNCRSA, PublicKey: "key", Fingerprint: "SHA256:key"}

	readRepo := newMemoryHostKeyRepo()
	readRepo.findErr = errors.New("database offline")
	_, err := New(readRepo).Check(ctx, presented)
	require.ErrorContains(t, err, "database offline")

	writeRepo := newMemoryHostKeyRepo()
	writeRepo.upsertErr = errors.New("disk full")
	err = New(writeRepo).Trust(ctx, presented, false)
	require.ErrorContains(t, err, "disk full")

	matchRepo := newMemoryHostKeyRepo(&host_key_entity.HostKey{
		Host: presented.Host, Port: presented.Port, KeyType: presented.KeyType,
		PublicKey: presented.PublicKey, Fingerprint: presented.Fingerprint,
	})
	matchRepo.upsertErr = errors.New("last-seen write failed")
	_, err = New(matchRepo).Check(ctx, presented)
	require.ErrorContains(t, err, "last-seen write failed")
}

func TestHostKeyServiceIsolatesKeyTypesAtSameEndpoint(t *testing.T) {
	repo := newMemoryHostKeyRepo(&host_key_entity.HostKey{
		Host: "shared.example", Port: 5900, KeyType: "ssh-rsa",
		PublicKey: "ssh-key", Fingerprint: "SHA256:ssh",
	})

	got, err := New(repo).Check(context.Background(), PresentedKey{
		Host: "shared.example", Port: 5900, KeyType: host_key_entity.KeyTypeVNCRSA,
		PublicKey: "vnc-key", Fingerprint: "SHA256:vnc",
	})

	require.NoError(t, err)
	require.Equal(t, CheckFirstUse, got.State)
	require.Empty(t, got.OldFingerprint)
}
