package tool

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
)

// fakeAssetRepo is a minimal in-memory AssetRepo for the CRUD tests below.
//
// The create-then-update flow under test needs Create to hand back a real, freshly
// assigned ID and FindByName/Find/Update to observe it afterwards — a gomock
// expectation list would have to hardcode the ID a fresh Create happens to assign,
// which defeats the point of testing create-then-update as one flow.
type fakeAssetRepo struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]*asset_entity.Asset
}

func newFakeAssetRepo() *fakeAssetRepo {
	return &fakeAssetRepo{byID: map[int64]*asset_entity.Asset{}}
}

func (r *fakeAssetRepo) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("asset %d not found", id)
	}
	return a, nil
}

func (r *fakeAssetRepo) FindByName(_ context.Context, name string) ([]*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*asset_entity.Asset
	for _, a := range r.byID {
		if a.Name == name {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *fakeAssetRepo) List(_ context.Context, opts asset_repo.ListOptions) ([]*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*asset_entity.Asset
	for _, a := range r.byID {
		if opts.Type != "" && a.Type != opts.Type {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *fakeAssetRepo) Create(_ context.Context, a *asset_entity.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	a.ID = r.nextID
	r.byID[a.ID] = a
	return nil
}

func (r *fakeAssetRepo) Update(_ context.Context, a *asset_entity.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[a.ID]; !ok {
		return fmt.Errorf("asset %d not found", a.ID)
	}
	r.byID[a.ID] = a
	return nil
}

func (r *fakeAssetRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

func (r *fakeAssetRepo) MoveToGroup(_ context.Context, _, _ int64) error  { return nil }
func (r *fakeAssetRepo) DeleteByGroupID(_ context.Context, _ int64) error { return nil }
func (r *fakeAssetRepo) FindByCredentialID(_ context.Context, _ int64) ([]*asset_entity.Asset, error) {
	return nil, nil
}
func (r *fakeAssetRepo) UpdateSortOrder(_ context.Context, _ int64, _ int) error { return nil }
func (r *fakeAssetRepo) UpdateGroupID(_ context.Context, _, _ int64) error       { return nil }
func (r *fakeAssetRepo) CountByTypes(_ context.Context, _ []string) (int64, error) {
	return 0, nil
}

// fakeGroupRepo mirrors fakeAssetRepo for groups, for the same reason.
type fakeGroupRepo struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]*group_entity.Group
}

func newFakeGroupRepo() *fakeGroupRepo {
	return &fakeGroupRepo{byID: map[int64]*group_entity.Group{}}
}

func (r *fakeGroupRepo) Find(_ context.Context, id int64) (*group_entity.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("group %d not found", id)
	}
	return g, nil
}

func (r *fakeGroupRepo) List(_ context.Context) ([]*group_entity.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*group_entity.Group, 0, len(r.byID))
	for _, g := range r.byID {
		out = append(out, g)
	}
	return out, nil
}

func (r *fakeGroupRepo) Create(_ context.Context, g *group_entity.Group) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	g.ID = r.nextID
	r.byID[g.ID] = g
	return nil
}

func (r *fakeGroupRepo) Update(_ context.Context, g *group_entity.Group) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[g.ID]; !ok {
		return fmt.Errorf("group %d not found", g.ID)
	}
	r.byID[g.ID] = g
	return nil
}

func (r *fakeGroupRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

func (r *fakeGroupRepo) UpdateName(_ context.Context, id int64, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byID[id]; ok {
		g.Name = name
	}
	return nil
}

func (r *fakeGroupRepo) ReparentChildren(_ context.Context, _, _ int64) error { return nil }

func (r *fakeGroupRepo) UpdateSortOrder(_ context.Context, id int64, sortOrder int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byID[id]; ok {
		g.SortOrder = sortOrder
	}
	return nil
}

func (r *fakeGroupRepo) UpdateParentID(_ context.Context, id, parentID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byID[id]; ok {
		g.ParentID = parentID
	}
	return nil
}

// crudTestEnv is the fixture shared by the TestHandlePut* tests below: an isolated,
// in-memory asset/group repo pair swapped in for the process-global singletons and
// restored on cleanup.
type crudTestEnv struct {
	ctx    context.Context
	assets *fakeAssetRepo
	groups *fakeGroupRepo
}

func setupCRUD(t *testing.T) *crudTestEnv {
	t.Helper()

	assets := newFakeAssetRepo()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

	groups := newFakeGroupRepo()
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(groups)
	t.Cleanup(func() { group_repo.RegisterGroup(origGroup) })

	return &crudTestEnv{ctx: context.Background(), assets: assets, groups: groups}
}

func (e *crudTestEnv) assetCount() int {
	e.assets.mu.Lock()
	defer e.assets.mu.Unlock()
	return len(e.assets.byID)
}

func (e *crudTestEnv) asset(name string) *asset_entity.Asset {
	e.assets.mu.Lock()
	defer e.assets.mu.Unlock()
	for _, a := range e.assets.byID {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func (e *crudTestEnv) groupCount() int {
	e.groups.mu.Lock()
	defer e.groups.mu.Unlock()
	return len(e.groups.byID)
}

func (e *crudTestEnv) group(name string) *group_entity.Group {
	e.groups.mu.Lock()
	defer e.groups.mu.Unlock()
	for _, g := range e.groups.byID {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// validOSSConfig builds the minimal config internal/assettype/oss.go's ValidateCreateArgs
// requires: endpoint + access_key_id. secret_access_key is deliberately omitted — supplying
// one would exercise credential_svc.Default().Encrypt, which needs a master key wired up
// that a package-local handler test has no reason to set up.
func (e *crudTestEnv) validOSSConfig() map[string]any {
	return map[string]any{
		"provider":      "s3",
		"endpoint":      "s3.us-east-1.amazonaws.com",
		"region":        "us-east-1",
		"access_key_id": "AKIAEXAMPLE",
	}
}

// 有 asset → 更新；无 asset → 创建。同一个工具，分支只由标识的有无决定。
func TestHandlePutAsset_CreateThenUpdate(t *testing.T) {
	env := setupCRUD(t)

	out, err := handlePutAsset(env.ctx, map[string]any{
		"name": "web-9", "type": "ssh",
		"config": map[string]any{"host": "10.0.0.9", "port": float64(22), "username": "root"},
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if env.assetCount() != 1 {
		t.Fatalf("expected exactly 1 asset after create, got %d", env.assetCount())
	}

	if _, err := handlePutAsset(env.ctx, map[string]any{
		"asset":  "web-9",
		"config": map[string]any{"username": "deploy"},
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if env.assetCount() != 1 {
		t.Errorf("put with an identifier must update in place, not create a second row (got %d)", env.assetCount())
	}
	sshCfg, err := env.asset("web-9").GetSSHConfig()
	if err != nil {
		t.Fatalf("GetSSHConfig: %v", err)
	}
	if got := sshCfg.Username; got != "deploy" {
		t.Errorf("username = %q, want %q", got, "deploy")
	}
	_ = out
}

// config 是自由对象，校验回到 assettype.ValidateCreateArgs——不是回到工具 schema。
func TestHandlePutAsset_ValidationComesFromAssetType(t *testing.T) {
	env := setupCRUD(t)

	_, err := handlePutAsset(env.ctx, map[string]any{
		"name": "broken", "type": "database",
		"config": map[string]any{"host": "10.0.0.1"}, // 缺 port/username/driver
	})
	if err == nil {
		t.Fatal("missing required config fields must fail")
	}
	if !strings.Contains(err.Error(), "driver") && !strings.Contains(err.Error(), "username") {
		t.Errorf("error %q should come from the asset type's own validation", err.Error())
	}
	if env.assetCount() != 0 {
		t.Errorf("a failed validation must not create anything, got %d assets", env.assetCount())
	}
}

// oss 类型此前被巨型 schema 完全遗漏（40 个属性里一个 oss 字段都没有），
// 自由 config 之后它必须可创建。
func TestHandlePutAsset_SupportsTypesTheOldSchemaOmitted(t *testing.T) {
	env := setupCRUD(t)

	if _, err := handlePutAsset(env.ctx, map[string]any{
		"name": "backup-bucket", "type": "oss",
		"config": env.validOSSConfig(), // 按 internal/assettype/oss.go 的必填字段构造
	}); err != nil {
		t.Fatalf("oss asset must be creatable via put_asset, got %v", err)
	}
}

// 未知类型必须报错并列出可用类型，而不是静默创建一个跑不起来的资产。
func TestHandlePutAsset_UnknownTypeIsNamed(t *testing.T) {
	env := setupCRUD(t)

	_, err := handlePutAsset(env.ctx, map[string]any{"name": "x", "type": "sqlite"})
	if err == nil || !strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("unknown type must be named in the error, got %v", err)
	}
}

func TestHandlePutGroup_CreateThenUpdate(t *testing.T) {
	env := setupCRUD(t)

	if _, err := handlePutGroup(env.ctx, map[string]any{"name": "prod"}); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := handlePutGroup(env.ctx, map[string]any{
		"id": float64(env.group("prod").ID), "description": "production fleet",
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if env.groupCount() != 1 {
		t.Errorf("put with an id must update in place, got %d groups", env.groupCount())
	}
	if got := env.group("prod").Description; got != "production fleet" {
		t.Errorf("description = %q, want %q", got, "production fleet")
	}
}
