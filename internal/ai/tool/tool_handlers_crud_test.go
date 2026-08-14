package tool

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
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

	// policyLookups, when set by setupCRUD, is incremented on every by-id Find — the
	// exact call permission.CheckPermission's internal asset_svc.Asset().Get makes to
	// load an asset's CommandPolicy. See crudTestEnv.policyChecks's doc comment for why
	// that specific call is a faithful "did anything consult policy/grant" signal.
	policyLookups *int
}

func newFakeAssetRepo() *fakeAssetRepo {
	return &fakeAssetRepo{byID: map[int64]*asset_entity.Asset{}}
}

func (r *fakeAssetRepo) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.policyLookups != nil {
		*r.policyLookups++
	}
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
		// Mirrors the real asset_repo.List's group filtering (see asset_repo/asset.go):
		// ExactGroupID matches the group id verbatim (including 0, for "ungrouped");
		// otherwise a positive GroupID still filters, 0 means "don't filter by group".
		if opts.ExactGroupID && a.GroupID != opts.GroupID {
			continue
		}
		if !opts.ExactGroupID && opts.GroupID > 0 && a.GroupID != opts.GroupID {
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

// MoveToGroup mirrors asset_repo.assetRepo.MoveToGroup (internal/repository/asset_repo/asset.go):
// every asset currently in fromGroupID moves to toGroupID. A no-op stub let
// TestHandleDeleteGroup_DefaultsToMovingAssetsOut pass while only ever checking the
// asset still existed, never that it actually left the deleted group — a regression
// that forgot to move assets out would have gone undetected.
func (r *fakeAssetRepo) MoveToGroup(_ context.Context, fromGroupID, toGroupID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.byID {
		if a.GroupID == fromGroupID {
			a.GroupID = toGroupID
		}
	}
	return nil
}

func (r *fakeAssetRepo) DeleteByGroupID(_ context.Context, groupID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, a := range r.byID {
		if a.GroupID == groupID {
			delete(r.byID, id)
		}
	}
	return nil
}
func (r *fakeAssetRepo) FindByCredentialID(_ context.Context, _ int64) ([]*asset_entity.Asset, error) {
	return nil, nil
}
func (r *fakeAssetRepo) UpdateSortOrder(_ context.Context, _ int64, _ int) error { return nil }
func (r *fakeAssetRepo) UpdateGroupID(_ context.Context, _, _ int64) error       { return nil }
func (r *fakeAssetRepo) CountByTypes(_ context.Context, _ []string) (int64, error) {
	return 0, nil
}
func (r *fakeAssetRepo) CountAgentAuthBySourceID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (r *fakeAssetRepo) CountAgentAuthBySourceIDGroupByFingerprint(_ context.Context, _ int64) (map[string]int64, error) {
	return nil, nil
}
func (r *fakeAssetRepo) ListAgentAuthBySourceID(_ context.Context, _ int64) ([]*asset_entity.Asset, error) {
	return nil, nil
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

// ReparentChildren mirrors group_repo.groupRepo.ReparentChildren
// (internal/repository/group_repo/group.go): every child of oldParentID is reparented
// to newParentID. group_svc.Delete always calls this before removing a group so its
// children don't end up dangling — a no-op stub can't catch a regression there.
func (r *fakeGroupRepo) ReparentChildren(_ context.Context, oldParentID, newParentID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.byID {
		if g.ParentID == oldParentID {
			g.ParentID = newParentID
		}
	}
	return nil
}

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

// crudTestEnv is the fixture shared by the TestHandlePut*/TestHandleDelete* tests
// below: an isolated, in-memory asset/group repo pair swapped in for the process-global
// singletons and restored on cleanup, plus a permission.CommandPolicyChecker wired into
// ctx so the delete tests can drive and observe the confirmation dialog.
type crudTestEnv struct {
	ctx    context.Context
	assets *fakeAssetRepo
	groups *fakeGroupRepo

	// confirmDecision drives the fake ConfirmFunc's response ("allow" / "deny"),
	// settable by the test *after* setupCRUD returns (the closure reads it live).
	// confirmCalls/lastConfirmKind/lastConfirmItems record what it was last called with.
	confirmDecision  string
	confirmCalls     int
	lastConfirmKind  string
	lastConfirmItems []permission.ApprovalItem

	// policyChecks mirrors fakeAssetRepo.policyLookups — see that field's doc comment.
	policyChecks int
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

	env := &crudTestEnv{assets: assets, groups: groups}
	assets.policyLookups = &env.policyChecks

	// group_svc.Group().Delete wraps its writes in dbutil.WithTransaction; without a
	// runner injected that falls through to a real GORM DB, which these tests don't
	// have. A passthrough runner makes it behave like a no-op wrapper, same as
	// group_svc's own tests (see group_test.go's setupTest).
	ctx := dbutil.WithTransactionRunner(context.Background(),
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })

	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
			env.confirmCalls++
			env.lastConfirmKind = kind
			env.lastConfirmItems = items
			return permission.ApprovalResponse{Decision: env.confirmDecision}
		})
	env.ctx = permission.WithPolicyChecker(ctx, checker)

	return env
}

// seedAsset directly inserts an SSH asset into the fake repo, bypassing asset_svc's
// validation — the delete tests operate on a target that must already exist, not one
// created through the tool under test.
func (e *crudTestEnv) seedAsset(name string, groupID int64) *asset_entity.Asset {
	a := &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeSSH, GroupID: groupID}
	if err := e.assets.Create(e.ctx, a); err != nil {
		panic(err)
	}
	return a
}

// seedGroup directly inserts a group into the fake repo, for the same reason.
func (e *crudTestEnv) seedGroup(name string) *group_entity.Group {
	g := &group_entity.Group{Name: name}
	if err := e.groups.Create(e.ctx, g); err != nil {
		panic(err)
	}
	return g
}

// grantEverything seeds (or reuses) the asset named "web-9" that the delete tests
// target and stuffs an allow-* command policy onto it — "往策略里塞一条 allow *". Delete
// must ignore this entirely: it never consults CommandPolicy or grant, so even the most
// permissive policy surface must not change whether the user gets prompted.
func (e *crudTestEnv) grantEverything() *asset_entity.Asset {
	a := e.asset("web-9")
	if a == nil {
		a = e.seedAsset("web-9", 0)
	}
	if err := a.SetCommandPolicy(&asset_entity.CommandPolicy{AllowList: []string{"*"}}); err != nil {
		panic(err)
	}
	return a
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
func TestHandlePutAsset_ManagedPasswordUsesSharedAtomicBoundary(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file:tool_put_managed?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	origAsset := asset_repo.Asset()
	origCredential := credential_repo.Credential()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("tool-put-master-key", []byte("tool-put-salt-16")))
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAsset)
		credential_repo.RegisterCredential(origCredential)
		sqlDB, sqlErr := gdb.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	plaintext := "ai-plaintext-must-not-leak"
	// #nosec G101 -- plaintext is an intentional test fixture used to verify that managed credentials never leak.
	out, err := handlePutAsset(context.Background(), map[string]any{
		"name": "cache-prod", "type": "redis", "credential_name": "managed-cache-login",
		"config": map[string]any{"host": "redis.internal", "username": "default", "password": plaintext},
	})
	require.NoError(t, err)
	assert.NotContains(t, out, plaintext)
	assert.Contains(t, out, `"authentication":{"type":"password","ref":`)

	var result struct {
		ID             int64 `json:"id"`
		Authentication struct {
			Type string `json:"type"`
			Ref  int64  `json:"ref"`
		} `json:"authentication"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Positive(t, result.ID)
	assert.Equal(t, credential_entity.TypePassword, result.Authentication.Type)

	cred, err := credential_repo.Credential().Find(context.Background(), result.Authentication.Ref)
	require.NoError(t, err)
	assert.Equal(t, "managed-cache-login", cred.Name)
	assert.Equal(t, "default", cred.Username)
	decrypted, err := credential_svc.Default().Decrypt(cred.Password)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)

	asset, err := asset_repo.Asset().Find(context.Background(), result.ID)
	require.NoError(t, err)
	cfg, err := asset.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, cred.ID, cfg.CredentialID)
	assert.Empty(t, cfg.Password)
}

func TestHandlePutAsset_ManagedCredentialInputFailuresLeaveNoAsset(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file:tool_put_invalid_managed?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	origAsset := asset_repo.Asset()
	origCredential := credential_repo.Credential()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("tool-put-master-key", []byte("tool-put-salt-16")))
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAsset)
		credential_repo.RegisterCredential(origCredential)
		sqlDB, sqlErr := gdb.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	key := &credential_entity.Credential{Name: "wrong-kind", Type: credential_entity.TypeSSHKey, PrivateKey: "ciphertext", PublicKey: "ssh-ed25519 AAAA", KeyType: credential_entity.KeyTypeED25519}
	require.NoError(t, credential_repo.Credential().Create(context.Background(), key))

	for _, config := range []map[string]any{
		{"host": "redis.internal", "username": "default", "credential_id": float64(key.ID)},
		{"host": "redis.internal", "username": "default", "credential_id": float64(key.ID), "password": "conflicting-secret"},
	} {
		_, err := handlePutAsset(context.Background(), map[string]any{"name": "invalid", "type": "redis", "config": config})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "conflicting-secret")
	}
	var count int64
	require.NoError(t, gdb.Model(&asset_entity.Asset{}).Count(&count).Error)
	assert.Zero(t, count)
}

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

func TestHandlePutAsset_SSHAuthenticationSwitchesRemainManagedAndSafe(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file:tool_put_ssh_switch?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &credential_entity.Credential{}, &ssh_agent_source_entity.SSHAgentSource{}))
	db.SetDefault(gdb)
	origAsset := asset_repo.Asset()
	origCredential := credential_repo.Credential()
	origAgentSource := ssh_agent_source_repo.SSHAgentSource()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	credential_svc.SetDefault(credential_svc.New("tool-put-master-key", []byte("tool-put-salt-16")))
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAsset)
		credential_repo.RegisterCredential(origCredential)
		ssh_agent_source_repo.RegisterSSHAgentSource(origAgentSource)
		sqlDB, sqlErr := gdb.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	password := "ai-password-must-not-leak"
	created, err := handlePutAsset(context.Background(), map[string]any{
		"name": "ssh-switch", "type": "ssh",
		"config": map[string]any{"host": "ssh.internal", "username": "root", "password": password},
	})
	require.NoError(t, err)
	assert.NotContains(t, created, password)
	assert.Contains(t, created, `"authentication":{"type":"password","ref":`)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := gossh.MarshalPrivateKey(privateKey, "ai-key")
	require.NoError(t, err)
	privateKeyPEM := string(pem.EncodeToMemory(block))
	updated, err := handlePutAsset(context.Background(), map[string]any{
		"asset": "ssh-switch", "credential_name": "managed-ai-key",
		"config": map[string]any{"private_key": privateKeyPEM},
	})
	require.NoError(t, err)
	assert.NotContains(t, updated, privateKeyPEM)
	assert.Contains(t, updated, `"authentication":{"type":"ssh_key","ref":`)

	source := &ssh_agent_source_entity.SSHAgentSource{Name: "offline", EndpointType: "unix", Endpoint: "/offline.sock", Createtime: 1, Updatetime: 1}
	require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(context.Background(), source))
	agentUpdated, err := handlePutAsset(context.Background(), map[string]any{
		"asset": "ssh-switch",
		"config": map[string]any{
			"auth_type": "agent", "agent_source_id": float64(source.ID),
			"agent_key_fingerprint": validAgentFingerprintForTest(),
		},
	})
	require.NoError(t, err)
	assert.Contains(t, agentUpdated, fmt.Sprintf(`"authentication":{"type":"ssh_agent","ref":%d}`, source.ID))
	assert.NotContains(t, agentUpdated, password)
	assert.NotContains(t, agentUpdated, privateKeyPEM)

	var credentialCount int64
	require.NoError(t, gdb.Model(&credential_entity.Credential{}).Count(&credentialCount).Error)
	assert.Equal(t, int64(2), credentialCount, "AI replacement must retain the old managed password and key credentials")
	stored, err := asset_repo.Asset().Find(context.Background(), 1)
	require.NoError(t, err)
	cfg, err := stored.GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AuthTypeAgent, cfg.AuthType)
	assert.Zero(t, cfg.CredentialID)
	assert.Empty(t, cfg.Password)
}

func TestHandlePutAsset_PreservesExistingSSHPrivateKeyBehavior(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file:tool_put_ssh_key?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	origAsset := asset_repo.Asset()
	origCredential := credential_repo.Credential()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("tool-put-master-key", []byte("tool-put-salt-16")))
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAsset)
		credential_repo.RegisterCredential(origCredential)
		sqlDB, sqlErr := gdb.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := gossh.MarshalPrivateKey(privateKey, "ai-key")
	require.NoError(t, err)
	privateKeyPEM := string(pem.EncodeToMemory(block))

	out, err := handlePutAsset(context.Background(), map[string]any{
		"name": "ssh-key-box", "type": "ssh",
		"config": map[string]any{"host": "ssh.internal", "username": "root", "private_key": privateKeyPEM},
	})
	require.NoError(t, err)
	assert.NotContains(t, out, privateKeyPEM)
	var credentialCount int64
	require.NoError(t, gdb.Model(&credential_entity.Credential{}).Count(&credentialCount).Error)
	assert.Equal(t, int64(1), credentialCount)
	var asset asset_entity.Asset
	require.NoError(t, gdb.First(&asset).Error)
	cfg, err := asset.GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AuthTypeKey, cfg.AuthType)
	assert.Positive(t, cfg.CredentialID)
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

func TestHandlePutAsset_ConfigMustBeAnObject(t *testing.T) {
	tests := []struct {
		name   string
		config any
	}{
		{name: "string", config: "not-an-object"},
		{name: "array", config: []any{"host"}},
		{name: "number", config: float64(7)},
		{name: "null", config: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupCRUD(t)
			_, err := handlePutAsset(env.ctx, map[string]any{
				"name": "invalid-config", "type": "local", "config": tc.config,
			})
			if err == nil || !strings.Contains(err.Error(), "config") || !strings.Contains(err.Error(), "object") {
				t.Fatalf("config=%#v error = %v, want an explicit object-shape error", tc.config, err)
			}
			if env.assetCount() != 0 {
				t.Fatalf("invalid config created %d assets, want none", env.assetCount())
			}
		})
	}
}

func TestHandlePutAsset_InvalidConfigDoesNotPartiallyUpdateAsset(t *testing.T) {
	env := setupCRUD(t)
	asset := env.seedAsset("web-9", 0)
	asset.Description = "before"

	_, err := handlePutAsset(env.ctx, map[string]any{
		"asset": "web-9", "description": "must-not-apply", "config": "not-an-object",
	})
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("invalid update config error = %v, want explicit rejection", err)
	}
	if got := env.asset("web-9").Description; got != "before" {
		t.Fatalf("invalid config partially updated description to %q", got)
	}
}

// validAgentFingerprintForTest 构造一个规范大写 SHA256: 前缀的 32 字节指纹，
// 通过 asset_entity.ValidateFingerprintSHA256 校验。
func validAgentFingerprintForTest() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
}

// seedSSHAgentSourceForTest 用真实 in-memory SQLite 注册来源仓库并插入一个来源，
// 供 ApplyCreateArgs/ApplyUpdateArgs 的引用完整性校验（RequireSourceExists）命中。
func seedSSHAgentSourceForTest(t *testing.T) int64 {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ssh_agent_source_entity.SSHAgentSource{}, &asset_entity.Asset{}))
	db.SetDefault(gdb)
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	src := &ssh_agent_source_entity.SSHAgentSource{
		Name: "work", EndpointType: "environment", Endpoint: "SSH_AUTH_SOCK", Createtime: 1, Updatetime: 1,
	}
	require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(context.Background(), src))
	return src.ID
}

// TestHandlePutAsset_AgentSSHFieldsFlowSymmetric 覆盖规格「AI 资产增删改查对 Agent
// 认证 SSH 资产对称接收并返回 agent_source_id 和 agent_key_fingerprint」：create/
// update 经 assettype 处理器保存来源 ID + 规范指纹（引用完整性校验与桌面一致），
// get 的安全视图对称返回这两个字段，校验规则复用任务 5 的同一处理器。
func TestHandlePutAsset_AgentSSHFieldsFlowSymmetric(t *testing.T) {
	env := setupCRUD(t)
	sourceID := seedSSHAgentSourceForTest(t)
	fp := validAgentFingerprintForTest()

	out, err := handlePutAsset(env.ctx, map[string]any{
		"name": "box", "type": "ssh",
		"config": map[string]any{
			"host": "10.0.0.1", "port": float64(22), "username": "root",
			"auth_type":             "agent",
			"agent_source_id":       float64(sourceID),
			"agent_key_fingerprint": fp,
		},
	})
	require.NoError(t, err, "agent asset create failed: %v", err)
	_ = out

	sshCfg, err := env.asset("box").GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, "agent", sshCfg.AuthType)
	assert.Equal(t, sourceID, sshCfg.AgentSourceID)
	assert.Equal(t, fp, sshCfg.AgentKeyFingerprint)

	// get 的安全视图对称返回（不泄露端点/公钥/备注）。
	view := toSafeView(env.asset("box"))
	assert.Equal(t, sourceID, view.AgentSourceID)
	assert.Equal(t, fp, view.AgentKeyFingerprint)

	// update 换指纹（同一来源）同样走校验路径。
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i) + 1
	}
	fp2 := "SHA256:" + base64.RawStdEncoding.EncodeToString(raw)
	_, err = handlePutAsset(env.ctx, map[string]any{
		"asset": "box",
		"config": map[string]any{
			"agent_source_id":       float64(sourceID),
			"agent_key_fingerprint": fp2,
		},
	})
	require.NoError(t, err, "agent asset update failed: %v", err)
	sshCfg, err = env.asset("box").GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, fp2, sshCfg.AgentKeyFingerprint)
	assert.Equal(t, sourceID, sshCfg.AgentSourceID)
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

// 删除恒需确认：即使策略里有 allow * 的 grant，也必须弹确认。
// 实现方式决定了这一点——delete 根本不查策略/grant，直接调 ConfirmFunc。
func TestHandleDeleteAsset_AlwaysConfirmsAndIsNotGrantable(t *testing.T) {
	env := setupCRUD(t)
	env.grantEverything() // 往策略里塞一条 allow * ——对 delete 必须无效

	env.confirmDecision = "allow"
	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(env.ctx, slot)
	if _, err := handleDeleteAsset(ctx, map[string]any{"asset": "web-9"}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if env.confirmCalls != 1 {
		t.Fatalf("delete must always prompt exactly once, got %d prompts", env.confirmCalls)
	}
	if env.policyChecks != 0 {
		t.Errorf("delete must not consult policy/grant at all, got %d checks", env.policyChecks)
	}
	if env.assetCount() != 0 {
		t.Errorf("asset should be gone, %d left", env.assetCount())
	}
	// decisionFromApproval 把用户点头映射进审计的 decision 列；写反了会让一次真实
	// 允许的删除在 audit_logs 里记成 decision=deny，且没有任何测试会报错。
	if slot.Decision != aictx.Allow || slot.DecisionSource != aictx.SourceUserAllow {
		t.Errorf("recorded decision = %v/%q, want Allow/%q", slot.Decision, slot.DecisionSource, aictx.SourceUserAllow)
	}
}

// 用户拒绝 → 不删，且不报 Go error（模型据此自纠，而不是整轮中断）。
func TestHandleDeleteAsset_DenyKeepsTheAsset(t *testing.T) {
	env := setupCRUD(t)
	env.seedAsset("web-9", 0)
	env.confirmDecision = "deny"

	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(env.ctx, slot)
	out, err := handleDeleteAsset(ctx, map[string]any{"asset": "web-9"})
	if err != nil {
		t.Fatalf("a user denial is not a tool error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "denied") {
		t.Errorf("result must tell the model it was denied: %q", out)
	}
	// 仓内拒绝文案的惯例是明确制止后续动作（checker.go:270、SubmitGrant、
	// local_tool_gate.go:93 都是这个措辞），不是软弱的小写 "user denied ..."——模型
	// 读到软弱的措辞可能换个引用重试。
	if !strings.Contains(out, "USER DENIED") || !strings.Contains(strings.ToLower(out), "stop the current task") {
		t.Errorf("result must use the repo's stop-now denial phrasing, got %q", out)
	}
	if env.assetCount() != 1 {
		t.Error("denied delete must keep the asset")
	}
	// 一次被拒绝的删除若在 decisionFromApproval 里被记成 Allow，audit_logs 会把它写成
	// decision=allow/user_allow——审计上看起来像被批准了，且没有任何东西会因此报错。
	if slot.Decision != aictx.Deny || slot.DecisionSource != aictx.SourceUserDeny {
		t.Errorf("recorded decision = %v/%q, want Deny/%q", slot.Decision, slot.DecisionSource, aictx.SourceUserDeny)
	}
}

func TestHandleDeleteAsset_InvalidApprovalResponsesKeepTheAsset(t *testing.T) {
	for _, decision := range []string{"", "bogus", "ALLOW", "allowAll"} {
		name := decision
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			env := setupCRUD(t)
			env.seedAsset("web-9", 0)
			env.confirmDecision = decision

			slot := &aictx.CheckResult{}
			ctx := aictx.WithCheckResultSlot(env.ctx, slot)
			_, _ = handleDeleteAsset(ctx, map[string]any{"asset": "web-9"})

			if env.assetCount() != 1 {
				t.Fatalf("decision %q deleted the asset; invalid/delete allowAll responses must fail closed", decision)
			}
			if slot.Decision != aictx.Deny {
				t.Fatalf("decision %q recorded %v, want deny", decision, slot.Decision)
			}
		})
	}
}

// 审批项必须带上资产名与删除语义，审批弹窗上"删哪台"要一眼可见。
func TestHandleDeleteAsset_ApprovalItemNamesTheAsset(t *testing.T) {
	env := setupCRUD(t)
	env.seedAsset("web-9", 0)
	env.confirmDecision = "allow"

	_, _ = handleDeleteAsset(env.ctx, map[string]any{"asset": "web-9"})

	if env.lastConfirmKind != "delete" {
		t.Errorf("approval kind = %q, want %q (the frontend renders this one without an allow-all button)",
			env.lastConfirmKind, "delete")
	}
	item := env.lastConfirmItems[0]
	if item.AssetName != "web-9" {
		t.Errorf("approval item asset name = %q, want web-9", item.AssetName)
	}
	if !strings.Contains(item.Command, "delete") {
		t.Errorf("approval item must read as a delete, got %q", item.Command)
	}
}

// delete_group 默认非破坏性分支：资产移入未分组，不删。
func TestHandleDeleteGroup_DefaultsToMovingAssetsOut(t *testing.T) {
	env := setupCRUD(t)
	g := env.seedGroup("prod")
	env.seedAsset("worker-1", g.ID)
	env.confirmDecision = "allow"

	if _, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(env.group("prod").ID)}); err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if env.assetCount() == 0 {
		t.Error("delete_assets defaults to false — the group's assets must survive")
	}
	if env.groupCount() != 0 {
		t.Error("the group itself should be gone")
	}
	// 光"还在"不够——它必须真的移出了被删的分组，否则就是一条指向已经不存在的
	// group_id 的悬空引用。fakeAssetRepo.MoveToGroup 从前是空实现，这条断言测不出来。
	if got := env.asset("worker-1").GroupID; got != 0 {
		t.Errorf("survivor asset must move to ungrouped (group_id=0), got group_id=%d", got)
	}
}

// delete_group 删除一个有子分组的分组时，子分组要挂到被删分组的父级，而不是变成
// 悬空引用——group_svc.Delete 在事务里调 group_repo.ReparentChildren 做这件事。
// fakeGroupRepo.ReparentChildren 从前是空实现，这个行为在单元测试里从未被真正验证过。
func TestHandleDeleteGroup_ReparentsChildGroups(t *testing.T) {
	env := setupCRUD(t)
	root := env.seedGroup("root")
	mid := env.seedGroup("prod")
	mid.ParentID = root.ID
	child := env.seedGroup("prod-child")
	child.ParentID = mid.ID
	env.confirmDecision = "allow"

	if _, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(mid.ID)}); err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if got := env.group("prod-child").ParentID; got != root.ID {
		t.Errorf("child group must be reparented to the deleted group's parent, got parent_id=%d want %d", got, root.ID)
	}
}

// delete_group 拒绝 → 组和组内资产都不动。
func TestHandleDeleteGroup_DenyKeepsGroupAndAssets(t *testing.T) {
	env := setupCRUD(t)
	g := env.seedGroup("prod")
	env.seedAsset("worker-1", g.ID)
	env.confirmDecision = "deny"

	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(env.ctx, slot)
	out, err := handleDeleteGroup(ctx, map[string]any{"id": float64(g.ID)})
	if err != nil {
		t.Fatalf("a user denial is not a tool error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "denied") {
		t.Errorf("result must tell the model it was denied: %q", out)
	}
	// 与 TestHandleDeleteAsset_DenyKeepsTheAsset 同一条惯例断言。
	if !strings.Contains(out, "USER DENIED") || !strings.Contains(strings.ToLower(out), "stop the current task") {
		t.Errorf("result must use the repo's stop-now denial phrasing, got %q", out)
	}
	if env.groupCount() != 1 {
		t.Error("denied delete must keep the group")
	}
	if env.assetCount() != 1 {
		t.Error("denied delete must keep the group's assets")
	}
	if slot.Decision != aictx.Deny || slot.DecisionSource != aictx.SourceUserDeny {
		t.Errorf("recorded decision = %v/%q, want Deny/%q", slot.Decision, slot.DecisionSource, aictx.SourceUserDeny)
	}
}

// fakeAuditRepo captures WriteToolCall's rows in memory, mirroring the audit package's
// own memAuditRepo test helper (internal/ai/audit/audit_asset_ref_test.go) — that one
// isn't exported, and package tool can't import a _test.go file from another package.
type fakeAuditRepo struct {
	mu   sync.Mutex
	logs []*audit_entity.AuditLog
}

func (r *fakeAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return nil
}

func (r *fakeAuditRepo) List(_ context.Context, _ audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return nil, 0, nil
}

func (r *fakeAuditRepo) ListSessions(_ context.Context, _ int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

func setupFakeAuditRepo(t *testing.T) *fakeAuditRepo {
	t.Helper()
	repo := &fakeAuditRepo{}
	orig := audit_repo.Audit()
	audit_repo.RegisterAudit(repo)
	t.Cleanup(func() { audit_repo.RegisterAudit(orig) })
	return repo
}

// deleteAssets=true 连带删除组内资产，且逐条补 delete_asset 审计——删一个含多台机器的
// 分组不能只留一行审计（internal/app/system/asset.go 的 System.DeleteGroup 同一份写法，
// 这里是它在 AI 路径上的对照）。
func TestHandleDeleteGroup_CascadeDeletesAssetsAndAuditsEachOne(t *testing.T) {
	env := setupCRUD(t)
	repo := setupFakeAuditRepo(t)
	g := env.seedGroup("prod")
	env.seedAsset("worker-1", g.ID)
	env.seedAsset("worker-2", g.ID)
	env.confirmDecision = "allow"

	out, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(g.ID), "delete_assets": true})
	if err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if !strings.Contains(out, `"deleted_assets":2`) {
		t.Errorf("result must report how many assets were cascaded, got %q", out)
	}
	if env.assetCount() != 0 {
		t.Errorf("deleteAssets=true must remove the group's assets too, %d left", env.assetCount())
	}
	if env.groupCount() != 0 {
		t.Error("the group itself should be gone")
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.logs) != 2 {
		t.Fatalf("expected 2 delete_asset audit rows (one per cascaded asset), got %d", len(repo.logs))
	}
	for _, l := range repo.logs {
		if l.ToolName != "delete_asset" {
			t.Errorf("cascade audit row tool_name = %q, want delete_asset", l.ToolName)
		}
	}
}

// toolWriteFailure registers a gorm write failure callback on the shared DB, mirroring
// asset_put_svc's own registerWriteFailure: the commit-failure test below drives a real
// Prepare + Commit and needs the repository write to fail deterministically.
func toolWriteFailure(t *testing.T, gdb *gorm.DB, operation, table string) {
	t.Helper()
	name := fmt.Sprintf("tool_put_%s_%s", operation, table)
	callback := func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == table {
			_ = tx.AddError(errors.New(table + " write failed"))
		}
	}
	switch operation {
	case "create":
		require.NoError(t, gdb.Callback().Create().Before("gorm:create").Register(name, callback))
	case "update":
		require.NoError(t, gdb.Callback().Update().Before("gorm:update").Register(name, callback))
	default:
		t.Fatalf("unknown callback operation %q", operation)
	}
}

// setupPutAssetDB 把真实 in-memory SQLite 装成 asset/credential 仓库，供走完整
// Prepare+Commit 物化的 put_asset 测试使用（与既有 managed-password 测试同一套夹具）。
func setupPutAssetDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file:tool_put_audit_proj?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	origAsset := asset_repo.Asset()
	origCredential := credential_repo.Credential()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.SetDefault(credential_svc.New("tool-put-audit-master-key", []byte("tool-put-audit-salt")))
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAsset)
		credential_repo.RegisterCredential(origCredential)
		sqlDB, sqlErr := gdb.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return gdb
}

// TestHandlePutAsset_SuccessAuditProjectionOmitsWriteOnlyFieldsAndExecutionGetsOriginal
// 是 Task 8 的核心契约：put_asset 成功时，handler 记录 producer 投影（普通 config +
// 资产身份 + typed authentication ref），write-only 字段整体缺席；而实际执行仍拿到原值
// （managed credential 可解密回明文）。
func TestHandlePutAsset_SuccessAuditProjectionOmitsWriteOnlyFieldsAndExecutionGetsOriginal(t *testing.T) {
	setupPutAssetDB(t)
	plaintext := "ai-projection-must-not-leak"
	slot := aictx.NewAuditRequestSlot()
	ctx := aictx.WithAuditRequestSlot(context.Background(), slot)

	out, err := handlePutAsset(ctx, map[string]any{
		"name": "cache-proj", "type": "redis",
		"config": map[string]any{"host": "redis.internal", "username": "default", "password": plaintext},
	})
	require.NoError(t, err)
	assert.NotContains(t, out, plaintext)

	proj := aictx.GetAuditRequest(ctx)
	require.NotNil(t, proj, "successful put_asset must record a producer projection")
	encoded, err := json.Marshal(proj)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), plaintext)

	// authentication 是 AuthenticationRef 结构体，先经 JSON 归一成 map 再断言。
	var projJSON map[string]any
	require.NoError(t, json.Unmarshal(encoded, &projJSON))
	config, ok := projJSON["config"].(map[string]any)
	require.True(t, ok, "ordinary config must be preserved in the projection")
	assert.Equal(t, "redis.internal", config["host"])
	assert.Equal(t, "default", config["username"])
	_, hasPassword := config["password"]
	assert.False(t, hasPassword, "write-only password must be entirely absent, not redacted")
	auth, ok := projJSON["authentication"].(map[string]any)
	require.True(t, ok, "typed authentication ref must be preserved")
	assert.Equal(t, "password", auth["type"])
	assert.Positive(t, auth["ref"].(float64))

	// 执行仍收到原值：物化出的 managed credential 可解密回明文。
	cred, err := credential_repo.Credential().Find(context.Background(), int64(auth["ref"].(float64)))
	require.NoError(t, err)
	decrypted, err := credential_svc.Default().Decrypt(cred.Password)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// TestHandlePutAsset_PrepareFailureProjectsTopLevelOnly: prepare/validation 失败时没有
// producer 投影可用，必须只投影顶层非 config 字段（name/type/asset 等），绝不回退原始
// config（它可能携带 write-only 秘密）。
func TestHandlePutAsset_PrepareFailureProjectsTopLevelOnly(t *testing.T) {
	env := setupCRUD(t)
	slot := aictx.NewAuditRequestSlot()
	ctx := aictx.WithAuditRequestSlot(env.ctx, slot)

	_, err := handlePutAsset(ctx, map[string]any{
		"name": "broken", "type": "database",
		"config": map[string]any{"host": "10.0.0.1", "password": "prepare-secret-must-not-leak"},
	})
	require.Error(t, err, "database without driver must fail validation")

	proj := aictx.GetAuditRequest(ctx)
	require.NotNil(t, proj, "prepare failure must still record a projection")
	encoded, marshalErr := json.Marshal(proj)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encoded), "prepare-secret-must-not-leak")
	assert.NotContains(t, string(encoded), "config", "prepare failure must never project raw config")
	assert.Equal(t, "broken", proj["name"])
	assert.Equal(t, "database", proj["type"])
}

// TestHandlePutAsset_PrePrepareFailuresProjectTopLevelOnly: 进入 Prepare 之前的早期返回
// （putArgs 形状校验、创建缺 name、未知类型、更新 lookup 失败）在 runner 眼里同样是
// "没有投影"→ 回退原始 c.Input，把可能携带 write-only 秘密的 config 原样写进审计。
// handler 必须在任何校验/lookup 之前就投影顶层非 config 字段，且原始 args/config 必须
// 原样保留（override 只落在审计投影槽，绝不改写执行/UI/历史）。
func TestHandlePutAsset_PrePrepareFailuresProjectTopLevelOnly(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]any
		wantErrSub string
		wantKey    string // 投影里应保留的顶层字段（asset/name/type）
		wantValue  any
	}{
		{
			name:       "create missing name",
			args:       map[string]any{"type": "ssh", "config": map[string]any{"host": "10.0.0.1", "password": "missing-name-secret"}},
			wantErrSub: "name",
			wantKey:    "type",
			wantValue:  "ssh",
		},
		{
			name:       "create unsupported type",
			args:       map[string]any{"name": "x", "type": "sqlite", "config": map[string]any{"host": "10.0.0.1", "password": "unknown-type-secret"}},
			wantErrSub: "sqlite",
			wantKey:    "name",
			wantValue:  "x",
		},
		{
			name:       "config not an object",
			args:       map[string]any{"name": "bad", "type": "ssh", "config": "not-an-object"},
			wantErrSub: "config",
			wantKey:    "name",
			wantValue:  "bad",
		},
		{
			name:       "update lookup failure",
			args:       map[string]any{"asset": "no-such-box", "config": map[string]any{"host": "10.0.0.1", "password": "lookup-secret"}},
			wantErrSub: "no-such-box",
			wantKey:    "asset",
			wantValue:  "no-such-box",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupCRUD(t)
			slot := aictx.NewAuditRequestSlot()
			ctx := aictx.WithAuditRequestSlot(env.ctx, slot)

			origConfig := tc.args["config"] // 快照原始 config，调用后必须原样保留

			_, err := handlePutAsset(ctx, tc.args)
			require.Error(t, err, "pre-Prepare failure must error")
			assert.Contains(t, err.Error(), tc.wantErrSub)
			assert.Equal(t, origConfig, tc.args["config"], "original config must be unchanged")
			assert.Equal(t, tc.wantValue, tc.args[tc.wantKey], "original args must be unchanged")

			// 投影只留顶层非 config 字段，绝不带出 secret 或 config。
			proj := aictx.GetAuditRequest(ctx)
			require.NotNil(t, proj, "pre-Prepare failure must still record a projection")
			encoded, marshalErr := json.Marshal(proj)
			require.NoError(t, marshalErr)
			assert.NotContains(t, string(encoded), "secret", "projection must not leak write-only secret")
			assert.NotContains(t, string(encoded), "config", "pre-Prepare failure must never project raw config")
			assert.Equal(t, tc.wantValue, proj[tc.wantKey], "top-level identity must survive in the projection")
		})
	}
}

// TestPutAssetTopLevelAuditArgs_TypedFailClosedProjection 是 Task 11 的核心契约：
// put_asset 顶层非 config 字段的 Audit 投影必须类型 fail-closed。string 身份字段仅在
// 实际值为 string 时保留；map/slice/array/struct/pointer 与一切类型非法值整体省略——
// 藏在 name/type/description 等允许键下的嵌套秘密不能借此进入 Audit。
func TestPutAssetTopLevelAuditArgs_TypedFailClosedProjection(t *testing.T) {
	// #nosec G101 -- secret is an intentional test fixture used to verify that nested
	// secrets never enter the audit projection via an allowlisted key.
	secret := "nested-secret-must-not-leak"
	payload := struct {
		Password string `json:"password"`
	}{Password: secret}

	args := map[string]any{
		"asset":           map[string]any{"password": secret}, // map
		"name":            []any{"a", secret},                 // slice
		"type":            payload,                            // struct
		"description":     &payload,                           // pointer
		"icon":            map[string]any{"token": secret},    // map
		"credential_name": []any{secret},                      // slice
		"group_id":        map[string]any{"id": secret},       // 非法复合
		"config":          map[string]any{"password": secret}, // config 从不投影
	}
	proj := putAssetTopLevelAuditArgs(args)

	for _, key := range []string{"asset", "name", "type", "description", "icon", "credential_name", "group_id"} {
		_, ok := proj[key]
		assert.False(t, ok, "type-invalid composite %s must be omitted from the projection", key)
	}
	_, hasConfig := proj["config"]
	assert.False(t, hasConfig, "config is never projected")

	encoded, err := json.Marshal(proj)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), secret, "nested secret must not enter the projection via any allowlisted key")
}

// TestPutAssetTopLevelAuditArgs_PreservesTypeCorrectScalars: 类型正确的 string 身份字段
// 与数值 group_id 按原值保留；config 从不投影；投影是独立 map，原 args 不被改写。
func TestPutAssetTopLevelAuditArgs_PreservesTypeCorrectScalars(t *testing.T) {
	args := map[string]any{
		"asset":           "web-9",
		"name":            "prod",
		"type":            "ssh",
		"description":     "fleet",
		"icon":            "server",
		"credential_name": "managed-login",
		"group_id":        float64(7),
		"config":          map[string]any{"password": "keep-out"},
	}
	proj := putAssetTopLevelAuditArgs(args)

	assert.Equal(t, "web-9", proj["asset"])
	assert.Equal(t, "prod", proj["name"])
	assert.Equal(t, "ssh", proj["type"])
	assert.Equal(t, "fleet", proj["description"])
	assert.Equal(t, "server", proj["icon"])
	assert.Equal(t, "managed-login", proj["credential_name"])
	assert.Equal(t, float64(7), proj["group_id"])
	_, hasConfig := proj["config"]
	assert.False(t, hasConfig, "config must never be projected")
	// 原 args 原样保留（投影是独立 map）。
	assert.Equal(t, "prod", args["name"])
	assert.Equal(t, "keep-out", args["config"].(map[string]any)["password"])
}

// TestPutAssetTopLevelAuditArgs_GroupIDAcceptsBoundaryNumerics: group_id 只在值是工具边界
// 支持的数值标量（JSON float64 + 内部 int/int64/json.Number）时保留，且投影保持可安全
// JSON 编码。
func TestPutAssetTopLevelAuditArgs_GroupIDAcceptsBoundaryNumerics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "json float64", value: float64(7)},
		{name: "float", value: 7.5},
		{name: "int", value: 7},
		{name: "int64", value: int64(7)},
		{name: "json.Number", value: json.Number("7")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := putAssetTopLevelAuditArgs(map[string]any{"group_id": tc.value})
			v, ok := proj["group_id"]
			assert.True(t, ok, "group_id %T must be preserved", tc.value)
			assert.Equal(t, tc.value, v)
			_, err := json.Marshal(proj)
			assert.NoError(t, err, "projection must stay marshal-safe")
		})
	}
}

// TestPutAssetTopLevelAuditArgs_GroupIDOmitsUnsafeOrInvalid: 非有限 float64（NaN/±Inf）
// 与非法 json.Number 会让 audit middleware 的 json.Marshal 直接失败，必须省略；string
// 与复合值/布尔/空值不是工具边界支持的数值标量，同样省略。
func TestPutAssetTopLevelAuditArgs_GroupIDOmitsUnsafeOrInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "NaN", value: math.NaN()},
		{name: "+Inf", value: math.Inf(1)},
		{name: "-Inf", value: math.Inf(-1)},
		{name: "json.Number abc", value: json.Number("abc")},
		{name: "json.Number NaN", value: json.Number("NaN")},
		{name: "string", value: "7"},
		{name: "slice", value: []any{7}},
		{name: "map", value: map[string]any{"id": 7}},
		{name: "bool", value: true},
		{name: "nil", value: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proj := putAssetTopLevelAuditArgs(map[string]any{"group_id": tc.value})
			_, ok := proj["group_id"]
			assert.False(t, ok, "group_id %#v must be omitted", tc.value)
			encoded, err := json.Marshal(proj)
			require.NoError(t, err, "projection must never be unmarshalable due to group_id")
			assert.Equal(t, "{}", string(encoded))
		})
	}
}

// TestHandlePutAsset_TopLevelProjectionNeverMutatesOriginalArgs: 进入 Prepare 之前的早期
// 失败（update lookup）中，无论顶层投影如何取舍，原始 args 必须深度不变——投影只写入独立
// 的 audit 槽，绝不改写执行/UI/历史输入。
func TestHandlePutAsset_TopLevelProjectionNeverMutatesOriginalArgs(t *testing.T) {
	env := setupCRUD(t)
	slot := aictx.NewAuditRequestSlot()
	ctx := aictx.WithAuditRequestSlot(env.ctx, slot)

	// #nosec G101 -- secret is an intentional test fixture used to verify that the
	// top-level projection never mutates original args nor leaks nested secrets.
	secret := "deep-unchanged-secret"
	args := map[string]any{
		"asset":       "no-such-box",
		"name":        map[string]any{"password": secret},
		"type":        []any{"ssh"},
		"description": struct{ Token string }{Token: secret},
		"group_id":    float64(3),
		"config":      map[string]any{"password": secret, "host": "10.0.0.1"},
	}
	before, err := json.Marshal(args)
	require.NoError(t, err)

	_, err = handlePutAsset(ctx, args)
	require.Error(t, err, "update lookup of a missing asset must fail before Prepare")

	after, marshalErr := json.Marshal(args)
	require.NoError(t, marshalErr)
	assert.Equal(t, before, after, "original args must be deeply unchanged after an early pre-Prepare failure")

	proj := aictx.GetAuditRequest(ctx)
	require.NotNil(t, proj, "early failure must still record the top-level projection")
	encoded, marshalErr := json.Marshal(proj)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encoded), secret, "nested secret must not reach the audit projection")
	for _, key := range []string{"name", "type", "description"} {
		_, ok := proj[key]
		assert.False(t, ok, "type-invalid composite %s must be omitted", key)
	}
	assert.Equal(t, float64(3), proj["group_id"], "type-correct numeric group_id survives")
	_, hasConfig := proj["config"]
	assert.False(t, hasConfig, "config is never projected on early failure")
}

// TestHandlePutAsset_CommitFailureProjectsSafeAuditArgs: Prepare 成功、Commit/仓库失败时，
// 用 Prepared.SafeAuditArgs 投影 —— 保留普通 config 与身份，write-only 字段整体缺席。
func TestHandlePutAsset_CommitFailureProjectsSafeAuditArgs(t *testing.T) {
	gdb := setupPutAssetDB(t)
	toolWriteFailure(t, gdb, "create", "assets")
	slot := aictx.NewAuditRequestSlot()
	ctx := aictx.WithAuditRequestSlot(context.Background(), slot)

	_, err := handlePutAsset(ctx, map[string]any{
		"name": "cache-fail", "type": "redis",
		"config": map[string]any{"host": "redis.internal", "username": "default", "password": "commit-secret-must-not-leak"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")

	proj := aictx.GetAuditRequest(ctx)
	require.NotNil(t, proj, "commit failure must still record a projection")
	encoded, marshalErr := json.Marshal(proj)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encoded), "commit-secret-must-not-leak")
	assert.NotContains(t, string(encoded), "password")
	config, ok := proj["config"].(map[string]any)
	require.True(t, ok, "ordinary config preserved on commit failure")
	assert.Equal(t, "redis.internal", config["host"])
	assert.Equal(t, "cache-fail", proj["name"])
	assert.Equal(t, "redis", proj["type"])
}

// TestHandlePutAsset_CompositeConfigNeverLeaksIntoAuditProjection 钉住 AI put_asset 的
// producer 投影边界：config 中必填标量字段为复合值时在类型边界校验失败（错误与审计投影都
// 不含 secret）；可选审批字段的复合值在 Prepare 成功时也被 approvalView 整体省略，绝不进入
// 审计投影——嵌套 secret 不能借任何允许键从共享 Prepare 边界流向 Audit。
func TestHandlePutAsset_CompositeConfigNeverLeaksIntoAuditProjection(t *testing.T) {
	env := setupCRUD(t)
	// #nosec G101 -- 嵌套 secret 是故意用于证明复合值不能进入审计投影的夹具。
	secret := "nested-secret-must-not-leak"

	t.Run("required scalar composite fails validation without leaking", func(t *testing.T) {
		slot := aictx.NewAuditRequestSlot()
		ctx := aictx.WithAuditRequestSlot(env.ctx, slot)
		_, err := handlePutAsset(ctx, map[string]any{
			"name": "broken", "type": "redis",
			"config": map[string]any{"host": map[string]any{"password": secret}, "username": "default"},
		})
		require.Error(t, err, "composite required host must fail validation")
		assert.NotContains(t, err.Error(), secret)
		proj := aictx.GetAuditRequest(ctx)
		require.NotNil(t, proj, "prepare failure must still record the top-level projection")
		encoded, marshalErr := json.Marshal(proj)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(encoded), secret)
		assert.NotContains(t, string(encoded), "config")
	})

	t.Run("optional approval field composite omitted on success", func(t *testing.T) {
		slot := aictx.NewAuditRequestSlot()
		ctx := aictx.WithAuditRequestSlot(env.ctx, slot)
		_, err := handlePutAsset(ctx, map[string]any{
			"name": "box", "type": "ssh",
			"config": map[string]any{"host": "10.0.0.1", "port": float64(22), "username": "root", "auth_type": map[string]any{"password": secret}},
		})
		require.NoError(t, err, "composite under an optional approval field must not fail the create")
		proj := aictx.GetAuditRequest(ctx)
		require.NotNil(t, proj)
		encoded, marshalErr := json.Marshal(proj)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(encoded), secret)
		var projJSON map[string]any
		require.NoError(t, json.Unmarshal(encoded, &projJSON))
		config, ok := projJSON["config"].(map[string]any)
		require.True(t, ok, "ordinary config preserved in the audit projection")
		_, hasAuthType := config["auth_type"]
		assert.False(t, hasAuthType, "composite auth_type must be omitted from the audit projection")
	})
}
