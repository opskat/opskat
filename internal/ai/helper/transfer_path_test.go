package helper

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

// setupTransferPathRepo registers a mock AssetRepo and restores the previous one after
// the test, following the repo idiom in internal/ai/assetref/resolve_test.go.
func setupTransferPathRepo(t *testing.T) *mock_asset_repo.MockAssetRepo {
	t.Helper()
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

func TestParseTransferEndpoint_ByID(t *testing.T) {
	m := setupTransferPathRepo(t)
	want := &asset_entity.Asset{ID: 7, Name: "web-01", Type: asset_entity.AssetTypeSSH}
	// assetref.Resolve always tries FindByName first regardless of whether the ref
	// parses as numeric (see internal/ai/assetref/resolve.go), so the numeric ref "7"
	// still needs FindByName mocked or gomock fails with "unexpected call".
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(want, nil)

	asset, path, err := ParseTransferEndpoint(context.Background(), "7:/etc/hosts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset == nil || asset.ID != 7 {
		t.Fatalf("got asset %+v, want id 7", asset)
	}
	if path != "/etc/hosts" {
		t.Fatalf("got path %q, want /etc/hosts", path)
	}
}

func TestParseTransferEndpoint_ByName(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "web-01").Return([]*asset_entity.Asset{
		{ID: 8, Name: "web-01", Type: asset_entity.AssetTypeSSH},
	}, nil)

	asset, path, err := ParseTransferEndpoint(context.Background(), "web-01:/var/log/app.log")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset == nil || asset.ID != 8 {
		t.Fatalf("got asset %+v, want id 8", asset)
	}
	if path != "/var/log/app.log" {
		t.Fatalf("got path %q, want /var/log/app.log", path)
	}
}

// TestParseTransferEndpoint_PrefixContainingSlash covers a prefix shaped like a
// group-qualified reference ("group/name"). assetref.Resolve does not split on "/" itself
// -- it hands the whole prefix to FindByName as an opaque name -- so this locks down that
// the shared parser splits only on the first colon and passes the prefix through to
// assetref.Resolve unmodified, whatever it contains.
func TestParseTransferEndpoint_PrefixContainingSlash(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "prod/web-01").Return([]*asset_entity.Asset{
		{ID: 9, Name: "prod/web-01", Type: asset_entity.AssetTypeSSH},
	}, nil)

	asset, path, err := ParseTransferEndpoint(context.Background(), "prod/web-01:/etc/hosts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset == nil || asset.ID != 9 {
		t.Fatalf("got asset %+v, want id 9", asset)
	}
	if path != "/etc/hosts" {
		t.Fatalf("got path %q, want /etc/hosts", path)
	}
}

// TestParseTransferEndpoint_WindowsPathStaysLocal is the behaviour parseRemotePathCtx
// existed to preserve: "C:\windows" must resolve to a local path. The D15 guard means the
// prefix "C" is now looked up (to decide whether it names an asset), unlike the pre-D15
// code, which short-circuited on "no leading slash" before ever touching the repo -- but
// since no asset is named "C", the lookup misses and the path still stays local.
func TestParseTransferEndpoint_WindowsPathStaysLocal(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "C").Return(nil, nil)

	asset, path, err := ParseTransferEndpoint(context.Background(), `C:\windows`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil (local path)", asset)
	}
	if path != `C:\windows` {
		t.Fatalf("got path %q, want unchanged input", path)
	}
}

func TestParseTransferEndpoint_NoColonStaysLocal(t *testing.T) {
	setupTransferPathRepo(t) // no EXPECT() calls

	asset, path, err := ParseTransferEndpoint(context.Background(), "./local-file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil (local path)", asset)
	}
	if path != "./local-file.txt" {
		t.Fatalf("got path %q, want unchanged input", path)
	}
}

// TestParseTransferEndpoint_LeadingColonStaysLocal covers ":foo" -- a colon with nothing
// before it. Today's parseRemotePathCtx short-circuits on "idx <= 0" before ever looking
// at what follows the colon; this must stay true so a stray leading colon never triggers
// an asset lookup or the D15 guard.
func TestParseTransferEndpoint_LeadingColonStaysLocal(t *testing.T) {
	setupTransferPathRepo(t) // no EXPECT() calls

	asset, path, err := ParseTransferEndpoint(context.Background(), ":foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil (local path)", asset)
	}
	if path != ":foo" {
		t.Fatalf("got path %q, want unchanged input", path)
	}
}

// TestParseTransferEndpoint_MissingLeadingSlashErrorsWhenPrefixIsAsset is the D15 guard:
// the prefix resolves to a real asset, but the text after the colon does not start with
// "/". This must error naming the "<asset>:/<path>" form, not silently fall back to
// treating the whole string as a local path.
func TestParseTransferEndpoint_MissingLeadingSlashErrorsWhenPrefixIsAsset(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "web-01").Return([]*asset_entity.Asset{
		{ID: 8, Name: "web-01", Type: asset_entity.AssetTypeSSH},
	}, nil)

	asset, _, err := ParseTransferEndpoint(context.Background(), "web-01:etc/hosts")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil on error", asset)
	}
	if !strings.Contains(err.Error(), "web-01:/<path>") {
		t.Fatalf("error %q does not name the correct <asset>:/<path> form", err.Error())
	}
}

// TestParseTransferEndpoint_UnresolvablePrefixMissingSlashStaysLocal is the negative case
// for the D15 guard: the prefix does NOT resolve to any asset, so the guard must not
// fire -- this keeps ordinary strings like "note:todo" (no such asset) local instead of
// erroring.
func TestParseTransferEndpoint_UnresolvablePrefixMissingSlashStaysLocal(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "note").Return(nil, nil)

	asset, path, err := ParseTransferEndpoint(context.Background(), "note:todo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil (local path)", asset)
	}
	if path != "note:todo" {
		t.Fatalf("got path %q, want unchanged input", path)
	}
}

// TestParseTransferEndpoint_UnresolvablePrefixWithSlashStillErrors preserves today's
// behaviour: a leading "/" after the colon always commits to the "this is a remote path"
// interpretation, so an asset reference that fails to resolve is still an error, never a
// silent fallback to local (a typo'd remote path must not become a confusing "file not
// found" against a literal "unknown-asset:/etc/passwd" path on disk).
func TestParseTransferEndpoint_UnresolvablePrefixWithSlashStillErrors(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "unknown-asset").Return(nil, nil)

	asset, _, err := ParseTransferEndpoint(context.Background(), "unknown-asset:/etc/passwd")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil on error", asset)
	}
}

// TestParseTransferEndpoint_AmbiguousPrefixWithSlashErrors: an ambiguous asset name is a
// resolution failure like any other in the leading-"/" branch, so it must still error
// rather than silently resolve to one of the candidates or fall back to local.
func TestParseTransferEndpoint_AmbiguousPrefixWithSlashErrors(t *testing.T) {
	m := setupTransferPathRepo(t)
	m.EXPECT().FindByName(gomock.Any(), "db").Return([]*asset_entity.Asset{
		{ID: 3, Name: "db", Type: asset_entity.AssetTypeDatabase},
		{ID: 9, Name: "db", Type: asset_entity.AssetTypeDatabase},
	}, nil)

	asset, _, err := ParseTransferEndpoint(context.Background(), "db:/data")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if asset != nil {
		t.Fatalf("got asset %+v, want nil on error", asset)
	}
}
