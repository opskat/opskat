package assetref

import (
	"context"
	"errors"
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
	m.EXPECT().FindByName(gomock.Any(), "42").Return(nil, nil)

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
	m.EXPECT().FindByName(gomock.Any(), "web-1").Return([]*asset_entity.Asset{
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
	m.EXPECT().FindByName(gomock.Any(), "db").Return([]*asset_entity.Asset{
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
	m.EXPECT().FindByName(gomock.Any(), "nope").Return([]*asset_entity.Asset{}, nil)

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

// TestResolve_NumericIDAlsoMatchesName covers the case that motivated this fix:
// asset 42 is a real id, but a *different* asset is literally named "42". Silently
// preferring the id match would run commands against the wrong machine, so this
// must be reported as ambiguous, listing every candidate id (including 42).
func TestResolve_NumericIDAlsoMatchesName(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().Find(gomock.Any(), int64(42)).Return(
		&asset_entity.Asset{ID: 42, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil)
	m.EXPECT().FindByName(gomock.Any(), "42").Return([]*asset_entity.Asset{
		{ID: 99, Name: "42", Type: asset_entity.AssetTypeRedis},
	}, nil)

	_, err := Resolve(context.Background(), "42")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	amb, ok := err.(*ErrAmbiguous)
	if !ok {
		t.Fatalf("expected *ErrAmbiguous, got %T: %v", err, err)
	}
	if len(amb.IDs) != 2 {
		t.Fatalf("got %d ids, want 2", len(amb.IDs))
	}
	found42, found99 := false, false
	for _, id := range amb.IDs {
		if id == 42 {
			found42 = true
		}
		if id == 99 {
			found99 = true
		}
	}
	if !found42 || !found99 {
		t.Fatalf("expected ids to include both 42 and 99, got %v", amb.IDs)
	}
}

// TestResolve_NumericRefWithNoIDButMatchingName covers a numerically-named asset
// that has no corresponding id: today an id-lookup miss returns not-found, which
// would make such an asset unreachable. It must still resolve by name.
func TestResolve_NumericRefWithNoIDButMatchingName(t *testing.T) {
	m := setupRepo(t)
	m.EXPECT().Find(gomock.Any(), int64(42)).Return(nil, errors.New("record not found"))
	m.EXPECT().FindByName(gomock.Any(), "42").Return([]*asset_entity.Asset{
		{ID: 99, Name: "42", Type: asset_entity.AssetTypeRedis},
	}, nil)

	got, err := Resolve(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 99 {
		t.Fatalf("got id %d, want 99", got.ID)
	}
}
