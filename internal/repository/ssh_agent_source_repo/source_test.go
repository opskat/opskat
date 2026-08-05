package ssh_agent_source_repo

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
)

func setupRepo(t *testing.T) (context.Context, SSHAgentSourceRepo) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}))
	db.SetDefault(gdb)
	return context.Background(), New()
}

func newSource(name, endpointType, endpoint string) *ssh_agent_source_entity.SSHAgentSource {
	return &ssh_agent_source_entity.SSHAgentSource{
		Name:         name,
		EndpointType: endpointType,
		Endpoint:     endpoint,
		Createtime:   1,
		Updatetime:   1,
	}
}

func TestSSHAgentSourceRepo_CreateAndFind(t *testing.T) {
	ctx, r := setupRepo(t)
	src := newSource("work", "environment", "SSH_AUTH_SOCK")
	require.NoError(t, r.Create(ctx, src))
	assert.NotZero(t, src.ID)

	got, err := r.Find(ctx, src.ID)
	require.NoError(t, err)
	assert.Equal(t, "work", got.Name)
	assert.Equal(t, "environment", got.EndpointType)
	assert.Equal(t, "SSH_AUTH_SOCK", got.Endpoint)
	assert.Equal(t, int64(1), got.Createtime)
}

func TestSSHAgentSourceRepo_FindMissing(t *testing.T) {
	ctx, r := setupRepo(t)
	_, err := r.Find(ctx, 999)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "expected ErrRecordNotFound, got %v", err)
}

func TestSSHAgentSourceRepo_List(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Create(ctx, newSource("a", "environment", "SSH_AUTH_SOCK")))
	require.NoError(t, r.Create(ctx, newSource("b", "unix_socket", "/tmp/agent.sock")))

	list, err := r.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func TestSSHAgentSourceRepo_Update(t *testing.T) {
	ctx, r := setupRepo(t)
	src := newSource("a", "environment", "SSH_AUTH_SOCK")
	require.NoError(t, r.Create(ctx, src))

	src.Name = "a-renamed"
	src.Description = "desc"
	require.NoError(t, r.Update(ctx, src))

	got, err := r.Find(ctx, src.ID)
	require.NoError(t, err)
	assert.Equal(t, "a-renamed", got.Name)
	assert.Equal(t, "desc", got.Description)
}

func TestSSHAgentSourceRepo_Delete(t *testing.T) {
	ctx, r := setupRepo(t)
	src := newSource("a", "environment", "SSH_AUTH_SOCK")
	require.NoError(t, r.Create(ctx, src))

	require.NoError(t, r.Delete(ctx, src.ID))

	_, err := r.Find(ctx, src.ID)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "expected ErrRecordNotFound, got %v", err)
}
