package assetref

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

func setupRepo(t *testing.T) *mock_asset_repo.MockAssetRepo {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
	return m
}

func TestResolve_NumericID(t *testing.T) {
	m := setupRepo(t)
	want := &asset_entity.Asset{ID: 42, Name: "web-1", Type: asset_entity.AssetTypeSSH}
	m.EXPECT().Find(gomock.Any(), int64(42)).Return(want, nil)

	got, err := Resolve(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("got id %d, want 42", got.ID)
	}
}

func TestResolve_ByName(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis},
		{ID: 8, Name: "web-1", Type: asset_entity.AssetTypeSSH},
	}, nil)

	got, err := Resolve(context.Background(), "web-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 8 {
		t.Fatalf("got id %d, want 8", got.ID)
	}
}

func TestResolve_AmbiguousNameIsError(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{
		{ID: 3, Name: "db", Type: asset_entity.AssetTypeDatabase},
		{ID: 9, Name: "db", Type: asset_entity.AssetTypeDatabase},
	}, nil)

	_, err := Resolve(context.Background(), "db")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	amb, ok := err.(*ErrAmbiguous)
	if !ok {
		t.Fatalf("expected *ErrAmbiguous, got %T", err)
	}
	if len(amb.IDs) != 2 {
		t.Fatalf("got %d ids, want 2", len(amb.IDs))
	}
}

func TestResolve_NotFound(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*asset_entity.Asset{}, nil)

	if _, err := Resolve(context.Background(), "nope"); err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestResolve_EmptyRef(t *testing.T) {
	setupRepo(t)
	if _, err := Resolve(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty ref, got nil")
	}
}
