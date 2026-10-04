package custom_type_repo

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
)

func setupRepo(t *testing.T) (context.Context, CustomTypeRepo) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&custom_type_entity.CustomType{}))
	db.SetDefault(gdb)
	return context.Background(), New()
}

func newType(slug string) *custom_type_entity.CustomType {
	return &custom_type_entity.CustomType{
		Slug:     slug,
		Name:     slug,
		ExecMode: custom_type_entity.ExecModeHTTP,
		Fields:   []custom_type_entity.Field{{Name: "host", Required: true}, {Name: "token", Secret: true}},
		HTTP: &custom_type_entity.HTTPConfig{
			BaseURL: "https://{{host}}",
			Auth:    []custom_type_entity.AuthBinding{{Type: "header", Name: "Authorization", Values: []string{"{{token}}"}}},
		},
		DefaultPolicy: &policy.CommandPolicy{AllowList: []string{"GET *"}},
		Createtime:    1,
		Updatetime:    1,
	}
}

func TestCustomTypeRepo_CreateFindRoundTrip(t *testing.T) {
	ctx, r := setupRepo(t)
	ct := newType("grafana")
	require.NoError(t, r.Create(ctx, ct))
	require.NotZero(t, ct.ID)

	got, err := r.Find(ctx, ct.ID)
	require.NoError(t, err)
	assert.Equal(t, ct.Fields, got.Fields)
	assert.Equal(t, ct.HTTP, got.HTTP)
	assert.Nil(t, got.Command)
	assert.Equal(t, ct.DefaultPolicy, got.DefaultPolicy)

	bySlug, err := r.FindBySlug(ctx, "grafana")
	require.NoError(t, err)
	assert.Equal(t, ct.ID, bySlug.ID)
}

func TestCustomTypeRepo_FindMissing(t *testing.T) {
	ctx, r := setupRepo(t)
	_, err := r.Find(ctx, 42)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	_, err = r.FindBySlug(ctx, "nope")
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestCustomTypeRepo_ListUpdateDelete(t *testing.T) {
	ctx, r := setupRepo(t)
	a, b := newType("aaa"), newType("bbb")
	require.NoError(t, r.Create(ctx, a))
	require.NoError(t, r.Create(ctx, b))

	a.Name = "renamed"
	a.Fields = a.Fields[:1]
	require.NoError(t, r.Update(ctx, a))
	got, err := r.Find(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Name)
	assert.Len(t, got.Fields, 1)

	require.NoError(t, r.Delete(ctx, b.ID))
	list, err := r.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, a.ID, list[0].ID)
}
