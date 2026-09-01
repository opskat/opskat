package host_key_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFindByHostPortKeyTypeIsolatesKeyTypes(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&host_key_entity.HostKey{}))
	db.SetDefault(gdb)

	ctx := context.Background()
	repo := NewHostKey()
	for _, key := range []*host_key_entity.HostKey{
		{Host: "shared.example", Port: 5900, KeyType: "ssh-rsa", PublicKey: "ssh", Fingerprint: "SHA256:ssh"},
		{Host: "shared.example", Port: 5900, KeyType: host_key_entity.KeyTypeVNCRSA, PublicKey: "vnc", Fingerprint: "SHA256:vnc"},
	} {
		require.NoError(t, repo.Upsert(ctx, key))
	}

	sshKey, err := repo.FindByHostPortKeyType(ctx, "shared.example", 5900, "ssh-rsa")
	require.NoError(t, err)
	require.Equal(t, "ssh", sshKey.PublicKey)

	vncKey, err := repo.FindByHostPortKeyType(ctx, "shared.example", 5900, host_key_entity.KeyTypeVNCRSA)
	require.NoError(t, err)
	require.Equal(t, "vnc", vncKey.PublicKey)

	require.NoError(t, repo.UpdateLastSeen(ctx, vncKey.ID, 1234))
	updated, err := repo.FindByHostPortKeyType(ctx, "shared.example", 5900, host_key_entity.KeyTypeVNCRSA)
	require.NoError(t, err)
	require.Equal(t, "vnc", updated.PublicKey)
	require.Equal(t, "SHA256:vnc", updated.Fingerprint)
	require.Equal(t, int64(1234), updated.LastSeen)
}
